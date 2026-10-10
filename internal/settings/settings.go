// Package settings 存全局配置（单例）。
//
// 和 store.Store 分开写，是因为那个是「一堆同类对象」的泛型容器，
// 而全局设置就一份 —— 套 map 的壳只会让读写多绕一圈。
package settings

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Token 一个 API 令牌，给脚本 / 外部工具用。
// 只存哈希：明文只在生成的那一刻返回一次，之后谁（包括我们）都拿不回来。
// Prefix 是明文前 8 位，纯粹为了让人在列表里认出「这是哪一把」。
type Token struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`
	Hash      string     `json:"hash"`
	CreatedAt time.Time  `json:"createdAt"`
	LastUsed  *time.Time `json:"lastUsed,omitempty"`
}

// Settings 全局配置。改完立刻落盘；只有 ListenAddr 需要重启守护进程才生效。
type Settings struct {
	// ── 访问安全 ──
	// RequireAuth 关着的时候谁都能连。默认关是为了「刚装上就能用」，
	// 但监听非回环地址又没开它时，设置页会明确告警。
	RequireAuth bool `json:"requireAuth"`
	// TrustLoopback 本机回环免登录。自己在这台机器上用不该被密码挡着。
	TrustLoopback bool       `json:"trustLoopback"`
	PasswordHash  string     `json:"passwordHash,omitempty"`
	PasswordSetAt *time.Time `json:"passwordSetAt,omitempty"`
	SessionHours  int        `json:"sessionHours"`
	Tokens        []Token    `json:"tokens"`

	// ── 下载源 ──
	// 新建实例 / 换服务端时默认用哪个源。空表示让用户每次自己选。
	DefaultMirror string `json:"defaultMirror"`
	// 在线模组市场用的地址。默认 Modrinth 官方 API —— 国内直连常被 Cloudflare 拦，
	// 留一个可改的入口，用户换个镜像 / 代理就能用，而不是这块功能对他永远坏着。
	MarketBase string `json:"marketBase"`

	// ── 守护进程 ──
	// 改完要重启守护进程才生效（监听端口没法在运行中热切换而不丢连接）
	ListenAddr string `json:"listenAddr"`
}

func defaults() Settings {
	return Settings{
		RequireAuth:   false,
		TrustLoopback: true,
		SessionHours:  72,
		DefaultMirror: "direct",
		MarketBase:    "https://api.modrinth.com/v2",
		ListenAddr:    ":23333",
	}
}

// Store 设置的唯一持有者
type Store struct {
	mu   sync.Mutex
	path string
	cur  Settings
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dataDir, "settings.json"), cur: defaults()}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return s, nil // 还没有配置文件 = 用默认值，不算错误
	}
	// 配置坏了不能让守护进程起不来 —— 用默认值继续跑，但把坏文件留个副本方便排查
	if err := json.Unmarshal(b, &s.cur); err != nil {
		_ = os.WriteFile(s.path+".bad", b, 0o644)
		s.cur = defaults()
	}
	s.repair()
	return s, nil
}

// repair 兜住不合理的值。配置文件是可以被手改的，手改成 0 或负数不该让会话永不过期。
func (s *Store) repair() {
	d := defaults()
	if s.cur.SessionHours < 1 || s.cur.SessionHours > 24*30 {
		s.cur.SessionHours = d.SessionHours
	}
	if s.cur.ListenAddr == "" {
		s.cur.ListenAddr = d.ListenAddr
	}
	if s.cur.DefaultMirror == "" {
		s.cur.DefaultMirror = d.DefaultMirror
	}
	if s.cur.MarketBase == "" {
		s.cur.MarketBase = d.MarketBase
	}
	// 没设密码却开着 RequireAuth = 谁都进不来（包括你自己）。
	// 这种情况直接视为没开，比锁死强。
	if s.cur.RequireAuth && s.cur.PasswordHash == "" && len(s.cur.Tokens) == 0 {
		s.cur.RequireAuth = false
	}
}

func (s *Store) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := s.cur
	cp.Tokens = append([]Token(nil), s.cur.Tokens...)
	return cp
}

// Update 改完自动落盘。fn 返回错误就不写盘。
func (s *Store) Update(fn func(*Settings) error) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cur
	next.Tokens = append([]Token(nil), s.cur.Tokens...)
	if err := fn(&next); err != nil {
		return s.cur, err
	}
	if err := s.save(&next); err != nil {
		return s.cur, err
	}
	s.cur = next
	cp := s.cur
	cp.Tokens = append([]Token(nil), s.cur.Tokens...)
	return cp, nil
}

func (s *Store) save(v *Settings) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	// 先写临时文件再 rename：写一半断电也不会留下半个配置文件
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// ── 密码 ──

func (s *Store) HasPassword() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur.PasswordHash != ""
}

// SetPassword 传空串 = 清除密码。
// 清除时顺手把 RequireAuth 关掉，否则会变成「开着保护但没密码」的死局。
func (s *Store) SetPassword(pw string) error {
	if pw != "" && len(pw) < 6 {
		return fmt.Errorf("密码至少 6 位")
	}
	_, err := s.Update(func(v *Settings) error {
		if pw == "" {
			v.PasswordHash = ""
			v.PasswordSetAt = nil
			v.RequireAuth = false
			return nil
		}
		h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		now := time.Now()
		v.PasswordHash = string(h)
		v.PasswordSetAt = &now
		return nil
	})
	return err
}

// CheckPassword 用常量时间比较，避免从耗时差里猜密码。
func (s *Store) CheckPassword(pw string) bool {
	s.mu.Lock()
	h := s.cur.PasswordHash
	s.mu.Unlock()
	if h == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(h), []byte(pw)) == nil
}

// ── 令牌 ──

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// AddToken 生成一把新令牌，**明文只在这里返回一次**。
func (s *Store) AddToken(name string) (Token, string, error) {
	if name == "" {
		name = "未命名令牌"
	}
	plain := "sbx_" + randHex(20) // 前缀让人一眼看出这是本项目的令牌
	t := Token{
		ID:        "tk" + randHex(6),
		Name:      name,
		Prefix:    plain[:12],
		Hash:      hashToken(plain),
		CreatedAt: time.Now(),
	}
	_, err := s.Update(func(v *Settings) error {
		v.Tokens = append(v.Tokens, t)
		return nil
	})
	if err != nil {
		return Token{}, "", err
	}
	return t, plain, nil
}

func (s *Store) RemoveToken(id string) bool {
	ok := false
	_, err := s.Update(func(v *Settings) error {
		out := v.Tokens[:0]
		for _, t := range v.Tokens {
			if t.ID == id {
				ok = true
				continue
			}
			out = append(out, t)
		}
		v.Tokens = out
		return nil
	})
	return err == nil && ok
}

// CheckToken 校验明文令牌，命中就顺手记下最后使用时间。
func (s *Store) CheckToken(plain string) (Token, bool) {
	if plain == "" {
		return Token{}, false
	}
	want := hashToken(plain)

	s.mu.Lock()
	var found *Token
	for i := range s.cur.Tokens {
		// 令牌哈希也是秘密，同样用常量时间比
		if subtle.ConstantTimeCompare([]byte(s.cur.Tokens[i].Hash), []byte(want)) == 1 {
			cp := s.cur.Tokens[i]
			found = &cp
			break
		}
	}
	s.mu.Unlock()

	if found == nil {
		return Token{}, false
	}
	now := time.Now()
	// 最后一次使用时间每 10 分钟才落一次盘 —— 每个请求都写文件没必要
	if found.LastUsed == nil || now.Sub(*found.LastUsed) > 10*time.Minute {
		_, _ = s.Update(func(v *Settings) error {
			for i := range v.Tokens {
				if v.Tokens[i].ID == found.ID {
					t := now
					v.Tokens[i].LastUsed = &t
				}
			}
			return nil
		})
	}
	return *found, true
}
