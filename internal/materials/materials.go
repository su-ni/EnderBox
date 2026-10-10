// Package materials 物品的中文名与图标，离线自带。
//
// 为什么必须自己带一份：
// 插件写出来的物品 id 是 `minecraft:diamond_sword` 这种机器名。想显示成「钻石剑」，
// 只有 Mojang 自己的语言文件里有 —— 没有任何在线翻译服务认得这种键。
// 所以装面板的时候把语言表和贴图打包进来，运行时零网络、零延迟。
//
// 数据由 tools/matgen/build.py 生成（默认合并 1.20.1 与最新正式版，贴图两个版本都取）。
// 不要再手改 materials.json，改了下次生成就冲掉了。
package materials

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

//go:embed materials.json
var rawTable []byte

//go:embed textures.zip
var rawZip []byte

// Entry 一个物品的显示信息。
// EN 是兜底：中文表没覆盖到（新版本刚加的物品）时显示英文，比显示 minecraft:xxx 强。
type Entry struct {
	ZH   string `json:"zh"`
	EN   string `json:"en"`
	Icon string `json:"icon"`
}

type table struct {
	GeneratedAt     string           `json:"generatedAt"`
	LangVersions    []string         `json:"langVersions"`
	TextureVersions []string         `json:"textureVersions"`
	Items           map[string]Entry `json:"items"`
	Effects         map[string]Entry `json:"effects"`
	Enchants        map[string]Entry `json:"enchants"`
}

var (
	loadOnce sync.Once
	loaded   table
	zipped   *zip.Reader
	loadErr  error
)

func load() {
	loadOnce.Do(func() {
		if err := json.Unmarshal(rawTable, &loaded); err != nil {
			loadErr = err
			return
		}
		zipped, loadErr = zip.NewReader(bytes.NewReader(rawZip), int64(len(rawZip)))
	})
}

// LoadErr 素材表坏掉时不为 nil。坏了也要能跑 —— 只是名字显示不出来，不是致命错误。
func LoadErr() error {
	load()
	return loadErr
}

// Item 查一个物品。查不到返回 ok=false，调用方决定怎么兜底。
func Item(id string) (Entry, bool) {
	load()
	e, ok := loaded.Items[normID(id)]
	return e, ok
}

// Effect 查一个状态效果（minecraft:speed → 速度）。
func Effect(id string) (Entry, bool) {
	load()
	e, ok := loaded.Effects[normID(id)]
	return e, ok
}

// Enchant 查一个附魔（minecraft:sharpness → 锋利）。
func Enchant(id string) (Entry, bool) {
	load()
	e, ok := loaded.Enchants[normID(id)]
	return e, ok
}

// normID 统一成 `minecraft:xxx`。插件给的一直是带命名空间的，
// 但用户手填、老存档里可能只有后半段 —— 补上前缀比返回「查不到」有用。
func normID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return ""
	}
	if !strings.Contains(id, ":") {
		return "minecraft:" + id
	}
	return id
}

// Display 决定一个物品格子上写什么。
//
// 优先级：**物品自己的自定义名 > 中文名 > 英文名 > 裸 id**。
// 自定义名优先是刻意的：一把被玩家改名成「老婆」的剑，显示成「钻石剑」是错的。
// 最后退回裸 id 而不是留空 —— 空着等于没说，id 至少能让人去查。
func Display(id, customName string) string {
	if s := StripColor(customName); s != "" {
		return s
	}
	if e, ok := Item(id); ok {
		if e.ZH != "" {
			return e.ZH
		}
		if e.EN != "" {
			return e.EN
		}
	}
	return id
}

// StripColor 去掉 § 颜色码。
// 物品的自定义名里常带 §6 这种格式码，直接塞进 JSON 给前端会显示成一串乱码。
func StripColor(s string) string {
	if !strings.Contains(s, "§") {
		return strings.TrimSpace(s)
	}
	var b strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '§' {
			i++ // 跳过格式码那一位
			continue
		}
		b.WriteRune(runes[i])
	}
	return strings.TrimSpace(b.String())
}

// Icon 取一个物品的图标 PNG。没有就返回 ok=false，别给他一张占位图 ——
// 「这个物品没图标」和「图标长得像方块」是两回事，混在一起会让用户以为图错了。
func Icon(id string) ([]byte, bool) {
	load()
	e, ok := loaded.Items[normID(id)]
	if !ok || e.Icon == "" || zipped == nil {
		return nil, false
	}
	f, err := zipped.Open(e.Icon)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(f); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// IconName 只返回图标在 zip 里的路径（给前端拼 URL 用），不读字节。
func IconName(id string) string {
	load()
	if e, ok := loaded.Items[normID(id)]; ok {
		return e.Icon
	}
	return ""
}

// ErrNotLoaded 素材表没加载成功。
var ErrNotLoaded = errors.New("物品素材表没加载成功")
