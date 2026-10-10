package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 导入已有服务器：把一个已经在跑的服务端目录直接纳管，不复制、不移动任何文件。
//
// 和向导的区别：向导是「从零装一份」，导入是「这边本来就有一份，别让我重装」。
// 所以这里只做两件事：①尽量认出它是什么（类型 / 版本 / 启动文件 / 端口），
// ②把不确定的地方如实说出来（Notes），而不是猜一个值填进去假装很懂。

// ImportScan 对一个已有服务端目录的识别结果。只读，无副作用。
type ImportScan struct {
	Dir           string `json:"dir"`
	Name          string `json:"name"` // 建议的实例名（取目录名）
	Type          Type   `json:"type"`
	MCVersion     string `json:"mcVersion"`
	LoaderVersion string `json:"loaderVersion,omitempty"`
	JarPath       string `json:"jarPath"` // 相对实例目录，和实例配置同口径
	Port          int    `json:"port"`
	EULA          bool   `json:"eula"`
	HasWorld      bool   `json:"hasWorld"`
	// Candidates 目录里其它可作启动文件的东西（换一个用），相对路径。
	Candidates []string `json:"candidates"`
	// Notes 识别过程中的「说明」和「不确定项」。一条都没有说明很确定。
	Notes []string `json:"notes"`
}

// ImportRequest 导入请求。Scan 里的字段都可以被调用方覆盖 ——
// 用户在预览里改过的东西，不该被自动识别结果又盖回去。
type ImportRequest struct {
	Dir           string `json:"dir"`
	Name          string `json:"name,omitempty"`
	Type          Type   `json:"type,omitempty"`
	MCVersion     string `json:"mcVersion,omitempty"`
	LoaderVersion string `json:"loaderVersion,omitempty"`
	JarPath       string `json:"jarPath,omitempty"`
	Port          int    `json:"port,omitempty"`
	MinMem        int    `json:"minMem,omitempty"`
	MaxMem        int    `json:"maxMem,omitempty"`
	JavaPath      string `json:"javaPath,omitempty"`
}

var (
	// 从文件名里认游戏版本：1.21 / 1.21.4 这种。前面不能紧跟数字
	// （否则会把 "11.21" 里那截也当成版本）。允许前面是 "." ——
	// fabric 的文件名就是 fabric-server-mc.1.21.4-loader.0.16.9-launcher.1.0.1.jar，
	// 而且游戏版本排在最前面，取「第一个」命中就对了。
	mcInNameRe = regexp.MustCompile(`(?:^|[^\d])(1\.\d{1,2}(?:\.\d{1,2})?)`)
	// Forge 的版本目录名是 <mc>-<loader>，例如 1.20.1-47.4.10
	forgeVerRe = regexp.MustCompile(`^(\d+\.\d+(?:\.\d+)?)-(.+)$`)
)

// DetectImport 扫一个目录，尽量认出它是什么服务端。认不出的地方写进 Notes。
func DetectImport(dir string) (ImportScan, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ImportScan{}, fmt.Errorf("目录不合法：%w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return ImportScan{}, fmt.Errorf("打不开这个目录：%w", err)
	}
	if !st.IsDir() {
		return ImportScan{}, fmt.Errorf("这不是一个文件夹")
	}

	sc := ImportScan{
		Dir:        abs,
		Name:       filepath.Base(abs),
		Port:       25565,
		Candidates: []string{},
		Notes:      []string{},
		EULA:       EULAAccepted(Instance{Dir: abs}),
	}

	// ── 世界目录 ──
	for _, w := range []string{"world", "world_nether", "world_the_end", "worlds"} {
		if fi, err := os.Stat(filepath.Join(abs, w)); err == nil && fi.IsDir() {
			sc.HasWorld = true
			break
		}
	}

	// ── 启动文件：先看有没有认得出的「混合端」启动 jar，再看 Forge/NeoForge 的
	//    args.txt，最后才轮到根目录里那些通用名的 jar ──
	args := findArgsFiles(abs)
	jars := findServerJars(abs)
	for _, a := range args {
		sc.Candidates = append(sc.Candidates, a.rel)
	}
	for _, j := range jars {
		sc.Candidates = append(sc.Candidates, j)
	}

	// ⚠ 混合端（Arclight / Mohist 这类）第一次启动会**把 Forge 装进自己目录**，
	// 于是根目录里同时躺着一个真正的启动 jar 和一个 Forge 生成的
	// libraries/.../win_args.txt。args.txt 里只有 JVM 参数、**没有主类**
	// （主类写在 jar 的 MANIFEST 里），拿它当启动目标只会得到
	// 「Invalid or corrupt jarfile」—— 实测 Arclight 1.20.1 就是这样。
	// 所以：根目录里有认得出的混合端 jar 时，它必须压过 args.txt。
	hybrid := ""
	for _, j := range jars {
		if isHybridLauncherName(strings.ToLower(j)) {
			hybrid = j
			break
		}
	}

	switch {
	case hybrid != "":
		t, _ := classifyJar(hybrid)
		sc.JarPath = hybrid
		sc.Type = t
		sc.MCVersion = mcFromName(hybrid)
		// 混合端的 jar 名里通常不带游戏版本，但它刚装好的那个 Forge 目录名里
		// 明明白白写着（1.20.1-47.3.22）—— 白捡的版本号，别浪费。
		if sc.MCVersion == "" && len(args) > 0 {
			sc.MCVersion = args[0].mcVer
			sc.LoaderVersion = args[0].loaderVer
		}
	case len(args) > 0:
		a := args[0]
		sc.JarPath = a.rel
		sc.Type = a.kind
		sc.LoaderVersion = a.loaderVer
		sc.MCVersion = a.mcVer
		if a.kind == TypeNeoForge && sc.MCVersion != "" {
			sc.Notes = append(sc.Notes, "NeoForge 的游戏版本是从加载器版本号推出来的，请确认一下对不对。")
		}
	case len(jars) > 0:
		name, typ := pickJar(jars)
		sc.JarPath = name
		sc.Type = typ
		sc.MCVersion = mcFromName(name)
	}

	// 认不出游戏版本时再从 version.json 兜一次（原版 / Paper 会写这个文件）
	if sc.MCVersion == "" {
		sc.MCVersion = mcFromVersionJSON(abs)
	}
	// 代理端（Velocity / BungeeCord）本来就没有「游戏版本」这回事
	if sc.MCVersion == "" && !isProxy(sc.Type) {
		sc.Notes = append(sc.Notes, "没认出游戏版本，导入后请到「配置」页确认一下。")
	}

	if sc.JarPath == "" {
		sc.Type = TypeVanilla
		sc.JarPath = "server.jar"
		sc.Notes = append(sc.Notes,
			"这个目录里没找到可启动的服务端文件（*.jar，或 Forge/NeoForge 的 args.txt）。"+
				"导入后到「配置 → 服务端文件」里指定，或者点「自动下载服务端」。")
	}

	if p, ok := readServerPort(abs); ok {
		sc.Port = p
	}
	if !sc.EULA && !isProxy(sc.Type) {
		sc.Notes = append(sc.Notes, "还没同意 EULA —— 第一次启动会直接被服务端拒绝，导入后体检里会提示你一键同意。")
	}
	if !sc.HasWorld {
		sc.Notes = append(sc.Notes, "没看到世界目录（world/），这个目录可能还是个没开过的新服。")
	}

	return sc, nil
}

func isProxy(t Type) bool { return t == TypeVelocity || t == TypeBungee }

// isHybridLauncherName 认「混合端」的启动 jar 名：本体是 Forge/Fabric，
// 另外挂了一套 Bukkit 实现，所以它同时能装模组和插件。
//
// 这个名单只有一个来源：classifyJar（认类型）和 DetectImport（挑启动文件）
// 都调它 —— 两处一旦各写一份，将来改了一边忘了另一边，
// 就会出现「类型认出来了、启动文件却挑错」这种半对半错的怪状。
func isHybridLauncherName(low string) bool {
	switch {
	case strings.Contains(low, "mohist"), strings.Contains(low, "arclight"),
		strings.Contains(low, "catserver"), strings.Contains(low, "magma"),
		strings.Contains(low, "cardboard"), strings.Contains(low, "banner"):
		return true
	}
	return false
}

// hybridBaseType 混合端「本体」那一半的加载器：Cardboard 是 Fabric 系，其余是 Forge 系。
func hybridBaseType(low string) Type {
	if strings.Contains(low, "cardboard") {
		return TypeFabric
	}
	return TypeForge
}

// classifyJar 从 jar 文件名认类型和「像不像正经服务端」的优先级。
// rank 越小越优先：1=认得出名字的，2=通用名，3=不认识。
func classifyJar(name string) (Type, int) {
	low := strings.ToLower(name)
	switch {
	// Bukkit 系一家亲：这些都是 Bukkit API 的服务端，插件目录、EULA、启动方式
	// 完全一致，所以归成同一个类型。⚠ 但「兼容 Bukkit 插件」不等于「实时数据能用」——
	// Folia 就不行，那个差异在 CompanionSupportOf 里单独判（它是按 jar 名判的，
	// 因为 Folia 的启动方式和 Paper 一样，光看类型看不出来）。
	case strings.Contains(low, "paper"), strings.Contains(low, "purpur"),
		strings.Contains(low, "spigot"), strings.Contains(low, "craftbukkit"),
		strings.Contains(low, "folia"), strings.Contains(low, "leaves"):
		return TypePaper, 1
	// 混合端：本体是 Forge/Fabric，另外挂了一套 Bukkit 实现。
	//
	// 能一键新建的两个给**独立类型**（Mohist / Arclight）。这不是分类洁癖 ——
	// 类型是「服务端文件怎么下」的判据：归成 forge 的话，一条 Mohist 实例
	// 点「一键补齐服务端文件」会去下 Forge 安装器并跑 --installServer，
	// 那是另一套完全不同的东西（Mohist 下下来就能直接启动）。
	//
	// 其余混合端（CatServer / Magma / Cardboard / Banner）没有可用的官方下载源，
	// 面板建不出来、只会以「导入」形式出现，归到本体那一半就够了 ——
	// 反正它们也不会走「下载服务端文件」这条路。
	case strings.Contains(low, "mohist"):
		return TypeMohist, 1
	case strings.Contains(low, "arclight"):
		return TypeArclight, 1
	case isHybridLauncherName(low):
		return hybridBaseType(low), 1
	// ⚠ 故意不认通用的 forge / neoforge 关键词：1.12.2 的
	// `forge-1.12.2-14.23.5.2859-universal.jar` 也在这一网里，而它不是能直接启动的服务端，
	// 认成 rank 1 会把原来的 `minecraft_server.1.12.2.jar` 挤下去。Forge 系靠目录里的
	// args.txt 判定（见上面的分支），那条路本来就走得通。
	case strings.Contains(low, "fabric"):
		return TypeFabric, 1
	case strings.Contains(low, "velocity"):
		return TypeVelocity, 1
	case strings.Contains(low, "bungeecord"), strings.Contains(low, "bungee"):
		return TypeBungee, 1
	case strings.Contains(low, "server"), strings.Contains(low, "minecraft"):
		return TypeVanilla, 2
	default:
		return TypeVanilla, 3
	}
}

func pickJar(jars []string) (string, Type) {
	best := ""
	bestRank := 99
	bestType := TypeVanilla
	for _, j := range jars {
		t, r := classifyJar(j)
		if r < bestRank {
			best, bestRank, bestType = j, r, t
		}
	}
	return best, bestType
}

// findServerJars 只看实例目录**第一层**的 jar。
// 往 libraries/ 里递归找会把 Forge 的上百个库文件全捞出来，那不是「服务端文件」。
func findServerJars(abs string) []string {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !jarRe.MatchString(n) {
			continue
		}
		// 安装器是工具、不是能启动的服务端：拿它启动只会得到「Invalid or corrupt jarfile」
		if strings.HasSuffix(strings.ToLower(n), "-installer.jar") {
			continue
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

type argsEntry struct {
	rel       string
	kind      Type
	mcVer     string
	loaderVer string
}

// findArgsFiles 找 Forge / NeoForge 的启动参数文件。
// 这两家的服务端不是一个 jar，而是「java @args.txt」—— 所以启动文件本身是那个 .txt。
func findArgsFiles(abs string) []argsEntry {
	specs := []struct {
		kind Type
		base string
	}{
		{TypeForge, filepath.Join(abs, "libraries", "net", "minecraftforge", "forge")},
		{TypeNeoForge, filepath.Join(abs, "libraries", "net", "neoforged", "neoforge")},
	}
	var out []argsEntry
	for _, sp := range specs {
		for _, fname := range []string{"win_args.txt", "unix_args.txt"} {
			matches, _ := filepath.Glob(filepath.Join(sp.base, "*", fname))
			if len(matches) == 0 {
				continue
			}
			sort.Sort(sort.Reverse(sort.StringSlice(matches)))
			for _, m := range matches {
				rel, err := filepath.Rel(abs, m)
				if err != nil {
					continue
				}
				verDir := filepath.Base(filepath.Dir(m))
				e := argsEntry{rel: filepath.ToSlash(rel), kind: sp.kind, loaderVer: verDir}
				if sp.kind == TypeForge {
					if mm := forgeVerRe.FindStringSubmatch(verDir); len(mm) == 3 {
						e.mcVer = mm[1]
						e.loaderVer = mm[2]
					}
				} else {
					e.mcVer = inferNeoForgeMC(verDir)
				}
				out = append(out, e)
			}
			break // win 有了就不再看 unix
		}
	}
	return out
}

// inferNeoForgeMC NeoForge 的版本号形如 21.1.72 / 20.4.237，前两段就是游戏版本
// 的次版本和补丁号 → 1.21.1 / 1.20.4。这是推断，所以调用方会加一条 Notes 提醒。
func inferNeoForgeMC(ver string) string {
	parts := strings.Split(ver, ".")
	if len(parts) < 2 {
		return ""
	}
	a, err1 := strconv.Atoi(parts[0])
	b, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return ""
	}
	return fmt.Sprintf("1.%d.%d", a, b)
}

func mcFromName(name string) string {
	if m := mcInNameRe.FindStringSubmatch(name); len(m) == 2 {
		return m[1]
	}
	return ""
}

// mcFromVersionJSON 读服务端自己写的 version.json（原版 / Paper 都有）。
func mcFromVersionJSON(abs string) string {
	b, err := os.ReadFile(filepath.Join(abs, "version.json"))
	if err != nil {
		return ""
	}
	var v struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	if v.Name != "" {
		return v.Name
	}
	return v.ID
}

// readServerPort 读 server.properties 里的 server-port。
// 服务端实际监听的是这个值 —— 导入时按它填，用户才不会一进来就看到「端口不一致」。
func readServerPort(dir string) (int, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "server.properties"))
	if err != nil {
		return 0, false
	}
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, "=")
		if i <= 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(line[:i]), "server-port") {
			if n, err := strconv.Atoi(strings.TrimSpace(line[i+1:])); err == nil && n > 0 && n <= 65535 {
				return n, true
			}
			return 0, false
		}
	}
	return 0, false
}

// ── 导入 ────────────────────────────────────────────────────

// Import 把一个已有目录纳管成本实例。不复制、不移动文件。
func (m *Manager) Import(req ImportRequest) (*Instance, error) {
	abs, err := filepath.Abs(strings.TrimSpace(req.Dir))
	if err != nil {
		return nil, fmt.Errorf("目录不合法：%w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("打不开这个目录：%w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("这不是一个文件夹")
	}

	// 同一个目录不要纳管两次：两个实例指向同一份存档，启停和备份会互相打架。
	for _, exist := range m.st.All() {
		if sameDir(exist.Dir, abs) {
			return nil, fmt.Errorf("这个目录已经纳管过了（实例「%s」）", exist.Name)
		}
	}
	// 别让实例目录套住面板自己的数据目录 —— 否则「删除并清空文件」会把整个面板毁掉。
	if dirContains(abs, m.baseDir) {
		return nil, fmt.Errorf("这个目录包含 EnderBox 自己的数据目录，不能纳管")
	}

	sc, err := DetectImport(abs)
	if err != nil {
		return nil, err
	}

	inst := Instance{
		Dir:           abs,
		Name:          firstNonEmpty(req.Name, sc.Name),
		Type:          firstType(req.Type, sc.Type, TypeVanilla),
		MCVersion:     firstNonEmpty(req.MCVersion, sc.MCVersion),
		LoaderVersion: firstNonEmpty(req.LoaderVersion, sc.LoaderVersion),
		JarPath:       firstNonEmpty(req.JarPath, sc.JarPath, "server.jar"),
		JavaPath:      strings.TrimSpace(req.JavaPath),
		Port:          firstInt(req.Port, sc.Port, 25565),
		MinMem:        firstInt(req.MinMem, 1024),
		MaxMem:        firstInt(req.MaxMem, 4096),
	}
	if inst.MaxMem < inst.MinMem {
		inst.MinMem = inst.MaxMem
	}
	if err := m.Create(&inst); err != nil {
		return nil, err
	}
	return &inst, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstType(vals ...Type) Type {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstInt(vals ...int) int {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

// sameDir 判断两个路径是不是同一个目录（Windows 下路径大小写不敏感）。
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ca, err1 := filepath.Abs(a)
	cb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(ca), filepath.Clean(cb))
}

// dirContains parent 是不是包含了 child（含相等）。
// 用字符串前缀 + 分隔符边界来判断 —— 直接 HasPrefix 会让 F:\data2 被误判成在 F:\data 里。
func dirContains(parent, child string) bool {
	pa, err1 := filepath.Abs(parent)
	cb, err2 := filepath.Abs(child)
	if err1 != nil || err2 != nil {
		return false
	}
	p := strings.ToLower(filepath.Clean(pa))
	c := strings.ToLower(filepath.Clean(cb))
	if p == c {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(c, p+sep)
}
