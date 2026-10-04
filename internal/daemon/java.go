package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// JavaRuntime 一个本机可用的 Java 运行时
type JavaRuntime struct {
	Path    string `json:"path"`
	Version string `json:"version"` // 完整版本串，如 21.0.3
	Major   int    `json:"major"`   // 主版本号：8 / 17 / 21
	Vendor  string `json:"vendor"`
	Source  string `json:"source"` // managed（本程序托管）| path（PATH）| system（系统扫描）
	Arch    string `json:"arch"`
	Default bool   `json:"default"` // 是否 PATH 里那个 java
}

func javaExeName() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}

var versionLineRe = regexp.MustCompile(`version\s+"([^"]+)"`)

// probeJava 跑一次 `java -version`。JVM 习惯把版本写到 stderr，所以两边都得收。
// 探测失败（文件不是可执行文件 / 权限问题）返回空结果而不是报错 —— 扫到一堆垃圾路径不该让用户看到报错。
func probeJava(path string) (JavaRuntime, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "-version")
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return JavaRuntime{}, false
	}
	raw := out.String()
	m := versionLineRe.FindStringSubmatch(raw)
	if m == nil {
		return JavaRuntime{}, false
	}
	full := m[1]
	rt := JavaRuntime{Path: path, Version: full, Major: majorOf(full)}
	rt.Vendor = vendorOf(raw)
	rt.Arch = archOf(raw)
	return rt, true
}

// majorOf 处理两种写法：老 Java 是 1.8.0_402，新 Java 直接 21.0.3
func majorOf(v string) int {
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '_' || r == '-' || r == '+' })
	if len(parts) == 0 {
		return 0
	}
	first, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	if first == 1 && len(parts) > 1 {
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			return 1
		}
		return n
	}
	return first
}

func vendorOf(raw string) string {
	low := strings.ToLower(raw)
	switch {
	case strings.Contains(low, "temurin"):
		return "Eclipse Temurin"
	case strings.Contains(low, "graalvm"):
		return "GraalVM"
	case strings.Contains(low, "zulu"):
		return "Azul Zulu"
	case strings.Contains(low, "microsoft"):
		return "Microsoft OpenJDK"
	case strings.Contains(low, "oracle"):
		return "Oracle"
	case strings.Contains(low, "liberica"):
		return "BellSoft Liberica"
	case strings.Contains(low, "openjdk"):
		return "OpenJDK"
	case strings.Contains(low, "hotspot"):
		return "OpenJDK"
	}
	return "未知"
}

func archOf(raw string) string {
	if strings.Contains(strings.ToLower(raw), "64-bit") {
		return "64 位"
	}
	if strings.Contains(strings.ToLower(raw), "32-bit") {
		return "32 位"
	}
	return ""
}

// candidateDirs 收集所有值得试的 java 可执行文件位置
func (m *Manager) javaCandidates() []string {
	exe := javaExeName()
	seen := map[string]bool{}
	var out []string

	add := func(p string) {
		if p == "" {
			return
		}
		clean := filepath.Clean(p)
		key := strings.ToLower(clean)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, clean)
	}

	// 1. 本程序托管的运行时：runtimes/<名字>/bin/java
	//    允许一层嵌套，因为下载包解压出来常常是 runtimes/jdk-21/jdk-21.0.3/bin/java
	if roots, err := filepath.Glob(filepath.Join(m.baseDir, "runtimes", "*", "bin", exe)); err == nil {
		for _, r := range roots {
			add(r)
		}
	}
	if roots, err := filepath.Glob(filepath.Join(m.baseDir, "runtimes", "*", "*", "bin", exe)); err == nil {
		for _, r := range roots {
			add(r)
		}
	}

	// 2. JAVA_HOME
	if home := strings.TrimSpace(os.Getenv("JAVA_HOME")); home != "" {
		add(filepath.Join(home, "bin", exe))
	}

	// 3. PATH 里的 java —— 默认用这个
	if p, err := exec.LookPath("java"); err == nil {
		add(p)
	}

	// 4. 系统常见安装位置
	switch runtime.GOOS {
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
			if base == "" {
				continue
			}
			for _, vendor := range []string{
				"Java", "Eclipse Adoptium", "BellSoft", "Microsoft", "Zulu", "GraalVM", "OpenJDK", "Semeru", "Amazon Corretto",
			} {
				pat := filepath.Join(base, vendor, "*", "bin", exe)
				if hits, err := filepath.Glob(pat); err == nil {
					for _, h := range hits {
						add(h)
					}
				}
			}
		}
	default:
		for _, pat := range []string{
			"/usr/lib/jvm/*/bin/java",
			"/usr/lib/jvm/*/*/bin/java",
			"/opt/java/*/bin/java",
			"/Library/Java/JavaVirtualMachines/*/Contents/Home/bin/java",
		} {
			if hits, err := filepath.Glob(pat); err == nil {
				for _, h := range hits {
					add(h)
				}
			}
		}
	}
	return out
}

// JavaRuntimes 探测本机所有可用的 Java，并发跑（每个都要起一次 JVM，串行会很慢）
func (m *Manager) JavaRuntimes() []JavaRuntime {
	cands := m.javaCandidates()
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := make([]JavaRuntime, 0, len(cands))

	sem := make(chan struct{}, 4)
	for _, c := range cands {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			sem <- struct{}{}
			rt, ok := probeJava(p)
			<-sem
			if !ok {
				return
			}
			rt.Source = m.javaSource(p)
			mu.Lock()
			out = append(out, rt)
			mu.Unlock()
		}(c)
	}
	wg.Wait()

	if def, err := exec.LookPath("java"); err == nil {
		def = filepath.Clean(def)
		for i := range out {
			if strings.EqualFold(filepath.Clean(out[i].Path), def) {
				out[i].Default = true
				out[i].Source = "path"
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Default != out[j].Default {
			return out[i].Default
		}
		if out[i].Major != out[j].Major {
			return out[i].Major > out[j].Major
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func (m *Manager) javaSource(p string) string {
	clean := filepath.Clean(strings.ToLower(p))
	base := filepath.Clean(strings.ToLower(m.baseDir))
	if strings.HasPrefix(clean, base+string(filepath.Separator)) ||
		strings.HasPrefix(clean, base+"/") {
		return "managed"
	}
	if home := strings.TrimSpace(os.Getenv("JAVA_HOME")); home != "" {
		if strings.HasPrefix(clean, filepath.Clean(strings.ToLower(home))) {
			return "system"
		}
	}
	if p2, err := exec.LookPath("java"); err == nil && strings.EqualFold(filepath.Clean(p2), p) {
		return "path"
	}
	return "system"
}

// ResolveJava 按主版本号挑一个最合适的 Java：精确匹配优先，其次向上取更高版本
func (m *Manager) ResolveJava(wantMajor int) (JavaRuntime, error) {
	all := m.JavaRuntimes()
	if len(all) == 0 {
		return JavaRuntime{}, fmt.Errorf("本机未检测到可用的 Java 运行时")
	}
	for _, rt := range all {
		if rt.Major == wantMajor {
			return rt, nil
		}
	}
	for _, rt := range all {
		if rt.Major > wantMajor {
			return rt, nil
		}
	}
	return JavaRuntime{}, fmt.Errorf("已安装 Java 最高 %d，但需要 Java %d", all[0].Major, wantMajor)
}
