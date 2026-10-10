package fetch

import "strings"

// RemoteFile 反查回来的一个**文件**。字段名对齐 Modrinth 的 `files[]`，
// 也正好是 `modrinth.index.json` 里 `files[]` 要写的那几项。
type RemoteFile struct {
	URL      string            `json:"url"`
	Filename string            `json:"filename"`
	Size     int64             `json:"size"`
	Primary  bool              `json:"primary"`
	Hashes   map[string]string `json:"hashes"` // sha1 / sha512
}

// RemoteVersion 反查回来的一个**版本**。
//
// 和 `FileHashesToProjects` 的区别：那个只要 project_id（够查名字/图标了），
// 这个要**完整信息** —— 制作整合包得把 url / size / hashes / environment 都写进清单，
// 少一样收包那边就装不起来。
type RemoteVersion struct {
	ProjectID    string            `json:"project_id"`
	VersionID    string            `json:"id"`
	VersionNum   string            `json:"version_number"`
	Name         string            `json:"name"`
	GameVersions []string          `json:"game_versions"`
	Loaders      []string          `json:"loaders"`
	// Environment 决定这个文件该进哪个包：`client_only` / `dedicated_server_only` / `*`。
	// ⚠ 制作整合包靠它分流，所以**必须原样保留**，不能自己归一化。
	Environment string            `json:"environment"`
	Files       []RemoteFile `json:"files"`
}

// PrimaryFile 取主文件。一个版本可能挂多个文件（比如带 sources jar），
// 装的时候只该取 `primary: true` 那个；没有 primary 标记就退回第一个 ——
// 这是 Modrinth 文档的行为，不是我们发明的兜底。
func (m RemoteVersion) PrimaryFile() (RemoteFile, bool) {
	if len(m.Files) == 0 {
		return RemoteFile{}, false
	}
	for _, f := range m.Files {
		if f.Primary {
			return f, true
		}
	}
	return m.Files[0], true
}

// VersionFilesByHash 批量反查：sha1 → 该文件的完整版本信息。
//
// 与 `FileHashesToProjects` 同一套路（分批 / 去重 / 小写），只是解出来的结构更全。
// 两者都保留：只要 project_id 的场景（列表页显示名字图标）没必要多传一堆字段。
//
// ⚠⚠ **查不到的哈希不会出现在返回的 map 里** —— 这不是错误，是正常情况：
// 用户自己改过的 jar、没发在 Modrinth 上的模组本来就查不到。
// 实测过：给 2 个 hash（1 真 1 假），返回**只有 1 条**。
// 所以调用方必须按「请求集 − 返回集 = 本地自制模组」来处理，
// **不能**把缺失当成「确认没问题」。
func VersionFilesByHash(base string, sha1s []string) (map[string]RemoteVersion, error) {
	clean := make([]string, 0, len(sha1s))
	seen := map[string]bool{}
	for _, h := range sha1s {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		clean = append(clean, h)
	}
	if len(clean) == 0 {
		return map[string]RemoteVersion{}, nil
	}

	out := map[string]RemoteVersion{}
	// 官方上限 1000，取 500 留一倍余量：整合包动辄几百个模组，一批打过去风险太大
	// （超限/超时都会让整批白跑，而分包之后失败的只有那一批）。
	const chunk = 500
	for i := 0; i < len(clean); i += chunk {
		end := i + chunk
		if end > len(clean) {
			end = len(clean)
		}
		var res map[string]RemoteVersion
		body := map[string]any{"hashes": clean[i:end], "algorithm": "sha1"}
		if _, err := marketPostAny(marketBases(base), "/version_files", body, &res); err != nil {
			return nil, err
		}
		for h, v := range res {
			out[strings.ToLower(h)] = v
		}
	}
	return out, nil
}
