// Package modpack 处理 Minecraft「整合包」（modpack）。
//
// 本文件是升级/导入的**执行层**：把 BuildPlan 算出来的计划真正落到磁盘。
// 解析层（mrpack.go / packstate.go / diff.go）只做纯逻辑；这里才碰网络与文件系统，
// 所以「绝不碰用户文件」「zip-slip 再挡一次」这类带破坏性的安全底线必须在这里兜住 ——
// 解析层信的是包作者，执行层信的是磁盘现实。
package modpack

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Fetcher 把「下一个文件」这件事交出去。
//
// ⚠ 这里故意只留一个极薄的接口，真正的下载（建父目录、校验哈希、断点续传、可取消）
// 交给面板现有的下载管理器去实现并注入 —— 整合包动辄几十上百个模组，
// 没理由在执行层再写一条下载路径，那样续传/校验就又得重写一遍。
//
// shaKind 必须跟着传：索引里的哈希有 sha512/sha1 两种（包只给哪种就是哪种），
// 只传值不传种类，校验方就不知道该按什么算法算 —— 轻则校验永远不过，重则形同没校验。
type Fetcher interface {
	Fetch(ctx context.Context, url, dst, wantSHA, shaKind string, size int64) error
}

// Progress 执行过程中的进度，按契约直接序列化给前端。
// 集合字段（没有 bytes 概念）留空即可，前端只关心 filesDone/filesTotal/current。
type Progress struct {
	FilesDone  int    `json:"filesDone"`
	FilesTotal int    `json:"filesTotal"`
	BytesDone  int64  `json:"bytesDone"`
	BytesTotal int64  `json:"bytesTotal"`
	Current    string `json:"current"` // 当前文件（相对实例目录）
}

// ExecuteOptions 一次执行的全部输入。执行器**只做 Plan 里的事**，不替调用方做决定。
type ExecuteOptions struct {
	InstanceDir                    string // 落地根目录（所有写入都必须校验「仍在这个目录内」）
	PackPath                       string // .mrpack 本地路径（解 overrides/ 与 server-overrides/ 用）
	Plan                           *Plan  // 由 BuildPlan 产出
	Index                          *Index // 目标版本的索引（取下载地址/大小，并从它造新状态）
	Old                            *State // 升级前的状态；首次安装为 nil
	Slug, ProjectID, VersionNumber string
	Fetcher                        Fetcher
	OnProgress                     func(Progress)
}

// Result 执行结果。注意：**部分失败不算成功** —— Failed 非空时调用方必须如实告诉用户，
// 不能因为 state=done 就报「全部成功」（前端那个进度条就是这么翻车的）。
type Result struct {
	Added, Replaced, Removed, SkippedCount int
	// Skipped 明细（给「哪些没动、为什么」用）
	Skipped []string `json:"skipped"`
	// Failed 失败的路径 + 原因。**部分失败不算成功**，要如实报。
	Failed []string `json:"failed"`
	State  *State   `json:"state"` // 新的 packstate（调用方写回）
}

// Execute 把计划落到磁盘，返回升级之后的新状态。
//
// 顺序上的取舍：**先下新的、再删旧的**（覆盖文件穿插在中间）。
// 万一中途失败，旧文件还稳稳在磁盘上，服务端还能起来 ——
// 反过来先删后下，一旦下载崩在半路，服就彻底缺文件起不来了。
// 这对开服器是致命的，所以宁可多扛一会儿旧文件。
func Execute(ctx context.Context, o ExecuteOptions) (*Result, error) {
	if o.Plan == nil {
		return nil, errors.New("执行升级必须带一份 Plan")
	}
	if o.Index == nil {
		return nil, errors.New("执行升级必须带目标版本的 Index")
	}
	if o.Fetcher == nil {
		return nil, errors.New("执行升级必须注入一个 Fetcher")
	}

	res := &Result{Skipped: []string{}, Failed: []string{}}
	// 覆盖清单：从包里单独读出来，作为「目标版本覆盖文件」的清单。
	// 覆盖文件不在 modrinth.index.json 里，必须单独走一遍 zip 目录。
	overrides := []OverrideRef{}
	if o.PackPath != "" {
		ov, err := ReadOverrides(o.PackPath)
		if err != nil {
			return nil, err
		}
		overrides = ov
	}
	// 新状态从「目标索引 + 覆盖清单」起步：它天然就是「所有服务端文件/覆盖都装好」的样子。
	// 后面再按实际成败去修。
	st := FromIndex(o.Index, o.Slug, o.ProjectID, o.VersionNumber, overrides)
	if st.Overrides == nil {
		st.Overrides = []OverrideRef{}
	}

	// 失败的文件路径集合：最后要把它们从新状态里剔掉（没真装好就别记成装好了）。
	failed := map[string]bool{}         // 下载失败的模组
	failedOverride := map[string]bool{} // 解压失败的覆盖文件

	// 计划里哪些覆盖路径「要解出来」「要删」—— 决定 extractOverrides 动不动手。
	overrideWrite := map[string]bool{}
	overrideDelete := map[string]bool{}
	overrideAction := map[string]Action{}
	for _, c := range o.Plan.ToAdd {
		if isOverride(c.Path) {
			overrideWrite[c.Path] = true
			overrideAction[c.Path] = c.Action
		}
	}
	for _, c := range o.Plan.ToReplace {
		if isOverride(c.Path) {
			overrideWrite[c.Path] = true
			overrideAction[c.Path] = c.Action
		}
	}
	for _, c := range o.Plan.ToRemove {
		if isOverride(c.Path) {
			overrideDelete[c.Path] = true
		}
	}

	// 进度计数：下载 + 覆盖文件 + 删除都算「一个文件」的进度。
	total := len(o.Plan.ToAdd) + len(o.Plan.ToReplace) + len(o.Plan.ToRemove)
	prog := Progress{FilesTotal: total, BytesTotal: o.Plan.DownloadBytes}
	report := func() {
		if o.OnProgress != nil {
			o.OnProgress(prog)
		}
	}

	// 索引里按 path 查文件，拿下载地址与大小。
	byPath := map[string]File{}
	for i := range o.Index.Files {
		f := o.Index.Files[i]
		if f.Path != "" {
			byPath[f.Path] = f
		}
	}

	// finish 在返回前统一修正状态：失败的剔除、跳过的保留旧 sha。
	finish := func() {
		if len(res.Failed) == 0 {
			res.Failed = []string{}
		}
		kept := st.Files[:0]
		for _, f := range st.Files {
			if !failed[f.Path] {
				kept = append(kept, f)
			}
		}
		st.Files = kept
		// 解压失败的覆盖文件同理剔掉
		keptOv := st.Overrides[:0]
		for _, f := range st.Overrides {
			if !failedOverride[f.Path] {
				keptOv = append(keptOv, f)
			}
		}
		st.Overrides = keptOv
		// 跳过的（用户改过/占用）我们实际上没动它，状态里别假装装成了新版本。
		for _, c := range o.Plan.Skipped {
			if c.Old == nil {
				continue
			}
			for i := range st.Files {
				if st.Files[i].Path == c.Path {
					st.Files[i].SHA = c.Old.SHA
					st.Files[i].SHAKind = c.Old.SHAKind
				}
			}
			for i := range st.Overrides {
				if st.Overrides[i].Path == c.Path {
					st.Overrides[i].SHA = c.Old.SHA
					st.Overrides[i].SHAKind = c.Old.SHAKind
				}
			}
		}
		res.State = st
	}

	// ── 第一步：下载（仅模组的新增 + 变更；覆盖文件交给 extractOverrides）──
	for _, c := range append(append([]Change{}, o.Plan.ToAdd...), o.Plan.ToReplace...) {
		if isOverride(c.Path) {
			continue // 覆盖文件不在 CDN 上，走解压，不在这里下载
		}
		if err := ctx.Err(); err != nil {
			finish()
			return res, err // 用户/调用方取消，如实带着已做到的结果撤退
		}
		prog.Current = c.Path
		report()

		f, ok := byPath[c.Path]
		if !ok {
			// 计划指向了索引里没有的文件 —— 这不该发生（BuildPlan 出的计划），记失败继续。
			res.Failed = append(res.Failed, c.Path)
			failed[c.Path] = true
			prog.FilesDone++
			report()
			continue
		}
		url := f.DownloadURL()
		if url == "" {
			res.Failed = append(res.Failed, c.Path)
			failed[c.Path] = true
			prog.FilesDone++
			report()
			continue
		}
		dst, err := resolveWithin(o.InstanceDir, c.Path)
		if err != nil {
			// 路径穿越：这是安全事件，不继续，直接失败返回。
			finish()
			return res, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			res.Failed = append(res.Failed, c.Path)
			failed[c.Path] = true
			prog.FilesDone++
			report()
			continue
		}
		// 哈希与种类一起带给校验方：种类取「新索引里这份文件自己声明的」
		// （BuildPlan 造 FileRef 时按 File.SHA() 记的），不是我们挑的。
		wantSHA, shaKind := "", ""
		if c.New != nil {
			wantSHA, shaKind = c.New.SHA, c.New.SHAKind
		}
		if err := o.Fetcher.Fetch(ctx, url, dst, wantSHA, shaKind, f.FileSize); err != nil {
			if ctx.Err() != nil {
				finish()
				return res, ctx.Err()
			}
			// 下载失败**不中止整批**：一个模组下不来不该让另外 128 个白下。
			res.Failed = append(res.Failed, c.Path)
			failed[c.Path] = true
			prog.FilesDone++
			report()
			continue
		}
		if c.Action == ActionAdd {
			res.Added++
		} else {
			res.Replaced++
		}
		prog.FilesDone++
		report()
	}

	// ── 第二步：解压覆盖文件（只解计划里要增/改的，逐级校验 zip-slip）──
	if err := extractOverrides(ctx, o, res, &prog, report, overrideWrite, overrideDelete, overrideAction, failedOverride); err != nil {
		finish()
		return res, err
	}

	// ── 第三步：删除（只删计划里标记的）──
	for _, c := range o.Plan.ToRemove {
		if err := ctx.Err(); err != nil {
			finish()
			return res, err
		}
		prog.Current = c.Path
		report()

		dst, err := resolvePlanPath(o.InstanceDir, c.Path)
		if err != nil {
			finish()
			return res, err
		}
		err = os.Remove(dst)
		if err != nil {
			if os.IsNotExist(err) {
				// 文件本来就不在（用户自己删了，或前一次升级已删）—— 不算失败，
				// 照样从状态里抹掉，因为它确实不在了。
				removeFromState(st, c.Path)
				prog.FilesDone++
				report()
				continue
			}
			// 其它错误（权限、被占用…）→ 进 Failed，让整批以「部分完成」收场。
			res.Failed = append(res.Failed, c.Path)
			prog.FilesDone++
			report()
			continue
		}
		res.Removed++
		removeFromState(st, c.Path)
		prog.FilesDone++
		report()
	}

	// 计划里「跳过」的明细，原样交给前端展示。
	for _, c := range o.Plan.Skipped {
		res.Skipped = append(res.Skipped, c.Path)
	}
	res.SkippedCount = len(res.Skipped)

	finish()
	return res, nil
}

// resolveWithin 把「相对实例目录的路径」解析成绝对路径，并**强制校验它仍落在实例目录内**。
//
// 这是 zip-slip 的第二道防线：解析层 mrpack.go 已经挡过一次（safeRelPath），
// 但执行层不能只信解析层 —— 包是第三方下载来的，谁知道中间有没有被换过。
// 用 filepath.Rel 反向算一次，凡是结果以 ".." 开头（或就是 ".."）就说明它会逃出实例目录，拦掉。
func resolveWithin(root, rel string) (string, error) {
	cleaned := filepath.Clean(filepath.FromSlash(rel))
	if cleaned == "" || cleaned == "." {
		return "", fmt.Errorf("空路径被拦截：%q", rel)
	}
	abs := filepath.Join(root, cleaned)
	back, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("目标路径无法解析：%w", err)
	}
	if back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径穿越被拦截（%q 会落到实例目录之外）", rel)
	}
	return abs, nil
}

// resolvePlanPath 把计划里的一条路径解析成磁盘绝对路径。
// 模组路径直接拼实例根；覆盖文件要先去掉 overrides/、server-overrides/ 约定前缀
// （它们只是 mrpack 的归类，文件真实落点是 <实例根>/config/x.toml 这种）。
func resolvePlanPath(root, p string) (string, error) {
	rel := p
	if isOverride(p) {
		rel = DiskRelOfOverride(p)
	}
	return resolveWithin(root, rel)
}

// extractOverrides 从 PackPath 解出 overrides/ 与 server-overrides/ 下、**计划要求写的**条目，写进实例目录。
//
// 只动 writeSet（= 计划里 ToAdd/ToReplace 的覆盖路径）：skipped 的一个字节都不写，
// 待删的留给删除步骤处理。每个条目都单独走 resolveWithin 校验 —— zip 条目名可以是 "../../x"，
// 不校验就直接写等于把任意文件写到系统任何位置。遇到穿越的条目一律当作安全事件，整次失败返回。
func extractOverrides(ctx context.Context, o ExecuteOptions, res *Result, prog *Progress, report func(), writeSet, deleteSet map[string]bool, actionOf map[string]Action, failedOverride map[string]bool) error {
	if o.PackPath == "" {
		return nil
	}
	zr, err := zip.OpenReader(o.PackPath)
	if err != nil {
		return fmt.Errorf("打不开整合包（%s）：%w", o.PackPath, err)
	}
	defer func() { _ = zr.Close() }()

	for _, zf := range zr.File {
		if err := ctx.Err(); err != nil {
			return err // 取消：带着已解出来的部分结果撤退
		}
		name := zf.Name
		if !isOverride(name) {
			continue // 不属于覆盖文件的两个前缀，跳过
		}
		if strings.HasSuffix(name, "/") {
			continue // 目录条目
		}
		// 计划没要求写的覆盖文件（被跳过、或要删的）→ 一个字节都不要碰。
		if !writeSet[name] || deleteSet[name] {
			continue
		}
		// 去掉约定前缀才是磁盘上的真实位置；路径仍要过穿越校验。
		rel := DiskRelOfOverride(name)
		dst, err := resolveWithin(o.InstanceDir, rel)
		if err != nil {
			return err // 路径穿越：安全事件，不继续
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			res.Failed = append(res.Failed, name)
			failedOverride[name] = true
			prog.FilesDone++
			report()
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			res.Failed = append(res.Failed, name)
			failedOverride[name] = true
			prog.FilesDone++
			report()
			continue
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			res.Failed = append(res.Failed, name)
			failedOverride[name] = true
			prog.FilesDone++
			report()
			continue
		}
		// 覆盖文件的意义就是「覆盖」：已存在的同名文件直接写穿，不用先查用户有没有改过。
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			res.Failed = append(res.Failed, name)
			failedOverride[name] = true
			prog.FilesDone++
			report()
			continue
		}
		// 覆盖文件 sha 已在 FromIndex 时按包里那份记进状态，这里只管落盘，不重复记。
		// 计数并入 Added/Replaced，和模组走同一套口径（前端只按 path 展示，混在一起）。
		if a, ok := actionOf[name]; ok {
			if a == ActionAdd {
				res.Added++
			} else {
				res.Replaced++
			}
		}
		prog.FilesDone++
		report()
	}
	return nil
}

// removeFromState 从状态里删掉某个路径的引用（文件实际已不在磁盘上）。
func removeFromState(st *State, path string) {
	kept := st.Files[:0]
	for _, f := range st.Files {
		if f.Path != path {
			kept = append(kept, f)
		}
	}
	st.Files = kept
}
