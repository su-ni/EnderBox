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

// javaHomeRe 抠出 java.home。用它把 javapath 这类「转发器」换成真正的 java 可执行文件 ——
// Oracle 的 javapath\java.exe 会再拉一个子进程跑 JVM，我们盯着的 PID 就只是个壳：
// 内存读数小两个数量级（壳 10MB / 真身 1.3GB），Job Object 也有「子进程早于挂载」的竞态。
var javaHomeRe = regexp.MustCompile(`(?m)^\s*java\.home\s*=\s*(.+?)\s*$`)

// vendorPropRe 取权威厂商属性。判断厂商必须优先用它，不能对整份输出做子串扫描 ——
// 带上 -XshowSettings:properties 之后输出里五花八门（java.library.path 里就混着
// Microsoft.DesktopAppInstaller），一台 Oracle 机器会被误判成 Microsoft。
var vendorPropRe = regexp.MustCompile(`(?m)^\s*java\.vendor\s*=\s*(.+?)\s*$`)

// probeJava 跑一次 `java -version`。JVM 习惯把版本写到 stderr，所以两边都得收。
// 探测失败（文件不是可执行文件 / 权限问题）返回空结果而不是报错 —— 扫到一堆垃圾路径不该让用户看到报错。
func probeJava(path string) (JavaRuntime, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	// 一次拿到版本号和 java.home：两者都往 stderr 打，收一次就够
	cmd := exec.CommandContext(ctx, path, "-XshowSettings:properties", "-version")
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

	// 归一化到真身 java.home/bin/java。拿不到或文件不在就保持原样 ——
	// 这一步只是「更好」，失败不该让这个 Java 变得不可用。
	if hm := javaHomeRe.FindStringSubmatch(raw); hm != nil {
		real := filepath.Join(strings.TrimSpace(hm[1]), "bin", javaExeName())
		if st, err := os.Stat(real); err == nil && !st.IsDir() {
			rt.Path = real
		}
	}
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
	// 优先读 java.vendor 属性（权威、就一行、不会被别的属性带偏）；
	// 读不到才退回扫整份文本。加 -XshowSettings:properties 之后整份输出里
	// 什么都可能有，拿它直接做子串匹配会把厂商认错。
	low := ""
	if m := vendorPropRe.FindStringSubmatch(raw); m != nil {
		low = strings.ToLower(m[1])
	}
	if low == "" {
		low = strings.ToLower(raw)
	}
	// 各家 java.vendor 的真实写法：Oracle Corporation / Eclipse Adoptium /
	// Azul Systems, Inc. / Microsoft / Amazon.com Inc. / BellSoft / IBM Semeru / GraalVM Community
	switch {
	case strings.Contains(low, "temurin") || strings.Contains(low, "adoptium"):
		return "Eclipse Temurin"
	case strings.Contains(low, "graalvm"):
		return "GraalVM"
	case strings.Contains(low, "zulu") || strings.Contains(low, "azul"):
		return "Azul Zulu"
	case strings.Contains(low, "microsoft"):
		return "Microsoft OpenJDK"
	case strings.Contains(low, "corretto") || strings.Contains(low, "amazon"):
		return "Amazon Corretto"
	case strings.Contains(low, "semeru") || strings.Contains(low, "ibm"):
		return "IBM Semeru"
	case strings.Contains(low, "liberica") || strings.Contains(low, "bellsoft"):
		return "BellSoft Liberica"
	case strings.Contains(low, "oracle"):
		return "Oracle"
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
	// PATH 里那个就是「默认」。要在探测前记下来：probeJava 会把路径归一化到真身，
	// 归一化之后再拿去和 PATH 比就永远比不上了。
	def, _ := exec.LookPath("java")
	if def != "" {
		def = filepath.Clean(def)
	}

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
			if def != "" && strings.EqualFold(filepath.Clean(p), def) {
				rt.Default = true
				rt.Source = "path"
			}
			mu.Lock()
			out = append(out, rt)
			mu.Unlock()
		}(c)
	}
	wg.Wait()

	// 归一化之后，PATH 里的转发器和 JAVA_HOME 可能指向同一个 JDK —— 去重，
	// 否则设置页会列出一模一样的两行。
	uniq := make([]JavaRuntime, 0, len(out))
	at := map[string]int{}
	for _, rt := range out {
		k := strings.ToLower(filepath.Clean(rt.Path))
		if i, ok := at[k]; ok {
			if rt.Default && !uniq[i].Default {
				uniq[i].Default = true
				uniq[i].Source = "path"
			}
			continue
		}
		at[k] = len(uniq)
		uniq = append(uniq, rt)
	}
	out = uniq

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
