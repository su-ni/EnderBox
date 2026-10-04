package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Store 一个极简的 JSON 文件键值存储。
// M1 阶段用它起步，后续实例/备份规模上来再换 SQLite（纯 Go 的 modernc.org/sqlite）。
type Store[T any] struct {
	mu    sync.Mutex
	path  string
	items map[string]T
}

func New[T any](path string) (*Store[T], error) {
	s := &Store[T]{path: path, items: map[string]T{}}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &s.items); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) && err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store[T]) All() []T {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]T, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	return out
}

func (s *Store[T]) Get(id string) (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.items[id]
	return v, ok
}

func (s *Store[T]) Put(id string, v T) error {
	s.mu.Lock()
	s.items[id] = v
	s.mu.Unlock()
	return s.flush()
}

func (s *Store[T]) Delete(id string) error {
	s.mu.Lock()
	delete(s.items, id)
	s.mu.Unlock()
	return s.flush()
}

func (s *Store[T]) flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
