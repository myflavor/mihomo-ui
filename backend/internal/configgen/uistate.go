package configgen

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// UIState holds panel preferences that always win over base/sub when installing.
type UIState struct {
	Mode      string `json:"mode,omitempty"`      // rule | global | direct
	LogLevel  string `json:"logLevel,omitempty"`  // debug|info|warning|error|silent
	TunEnable *bool  `json:"tunEnable,omitempty"` // nil = leave base/sub as-is except default false on empty
}

type UIStateStore struct {
	path string
	mu   sync.Mutex
	cur  UIState
}

func NewUIStateStore(path string) (*UIStateStore, error) {
	s := &UIStateStore{path: path}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *UIStateStore) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.cur = UIState{}
			return s.saveLocked()
		}
		return err
	}
	if len(b) == 0 {
		s.cur = UIState{}
		return nil
	}
	var st UIState
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	s.cur = st
	return nil
}

func (s *UIStateStore) saveLocked() error {
	out, err := json.MarshalIndent(s.cur, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *UIStateStore) Get() UIState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

func (s *UIStateStore) SetMode(mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cur.Mode = mode
	return s.saveLocked()
}

func (s *UIStateStore) SetLogLevel(level string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cur.LogLevel = level
	return s.saveLocked()
}

func (s *UIStateStore) SetTunEnable(enable bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := enable
	s.cur.TunEnable = &v
	return s.saveLocked()
}

// Overlay applies ui-state on top of a merged config map.
func (st UIState) Overlay(root map[string]any) {
	if root == nil {
		return
	}
	if st.Mode != "" {
		root["mode"] = st.Mode
	}
	if st.LogLevel != "" {
		root["log-level"] = st.LogLevel
	}
	if st.TunEnable != nil {
		tun := asMap(root["tun"])
		if tun == nil {
			tun = map[string]any{}
		} else {
			tun = cloneMap(tun)
		}
		tun["enable"] = *st.TunEnable
		root["tun"] = tun
	}
}
