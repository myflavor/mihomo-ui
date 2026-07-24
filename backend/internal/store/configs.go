package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// Time is wall-clock local time, stored as "2006-01-02 15:04:05" in YAML/JSON.
type Time struct {
	time.Time
}

const timeLayout = "2006-01-02 15:04:05"

func Now() Time {
	return Time{Time: time.Now()}
}

func (t Time) MarshalYAML() (any, error) {
	if t.Time.IsZero() {
		return "", nil
	}
	return t.Time.Local().Format(timeLayout), nil
}

func (t *Time) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Kind != yaml.ScalarNode {
		t.Time = time.Time{}
		return nil
	}
	s := strings.TrimSpace(value.Value)
	if s == "" || s == "null" {
		t.Time = time.Time{}
		return nil
	}
	if parsed, err := time.ParseInLocation(timeLayout, s, time.Local); err == nil {
		t.Time = parsed
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, s); err == nil {
		t.Time = parsed
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339, s); err == nil {
		t.Time = parsed
		return nil
	}
	return fmt.Errorf("invalid time %q", s)
}

func (t Time) MarshalJSON() ([]byte, error) {
	if t.Time.IsZero() {
		return []byte(`""`), nil
	}
	return []byte(`"` + t.Time.Local().Format(timeLayout) + `"`), nil
}

func (t *Time) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		t.Time = time.Time{}
		return nil
	}
	if parsed, err := time.ParseInLocation(timeLayout, s, time.Local); err == nil {
		t.Time = parsed
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, s); err == nil {
		t.Time = parsed
		return nil
	}
	if parsed, err := time.Parse(time.RFC3339, s); err == nil {
		t.Time = parsed
		return nil
	}
	return fmt.Errorf("invalid time %q", s)
}

// Config is one saved config entry (URL subscription or local file).
type Config struct {
	ID     string `yaml:"id" json:"id"`
	Name   string `yaml:"name" json:"name"`
	URL    string `yaml:"url,omitempty" json:"url,omitempty"`
	Source string `yaml:"source" json:"source"` // url | file
	// Active is derived from configId; only exposed in API JSON.
	Active    bool `yaml:"-" json:"active"`
	Interval  int  `yaml:"interval" json:"interval"` // seconds; 0 = no auto for file
	UpdatedAt Time `yaml:"updatedAt" json:"updatedAt"`
	CreatedAt Time `yaml:"createdAt" json:"createdAt"`

	// legacy fields from older YAML
	Enabled      bool `yaml:"enabled,omitempty" json:"-"`
	LegacyActive bool `yaml:"active,omitempty" json:"-"`
}

// UIPrefs are panel switches stored in settings.yaml.
type UIPrefs struct {
	Mode      string `yaml:"mode" json:"mode"`
	LogLevel  string `yaml:"log-level" json:"log-level"`
	TunEnable bool   `yaml:"tun-enable" json:"tun-enable"`
}

// settingsFile is the on-disk ui/settings.yaml layout.
type settingsFile struct {
	Mode      string   `yaml:"mode"`
	LogLevel  string   `yaml:"log-level"`
	TunEnable bool     `yaml:"tun-enable"`
	ConfigID  string   `yaml:"configId"`
	Configs   []Config `yaml:"configs"`

	// legacy keys (migrate once then drop)
	ActiveID string   `yaml:"activeId,omitempty"`
	Items    []Config `yaml:"items,omitempty"`
}

type Store struct {
	path     string
	mu       sync.Mutex
	prefs    UIPrefs
	configs  []Config
	configID string
}

// New opens ui/settings.yaml. If only legacy data.yaml exists beside it, merges once.
func New(path string, defaults UIPrefs) (*Store, error) {
	s := &Store{path: path, prefs: defaults}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := s.load(defaults); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load(defaults UIPrefs) error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// try migrate legacy data.yaml next to settings.yaml
		legacy := filepath.Join(filepath.Dir(s.path), "data.yaml")
		if lb, lerr := os.ReadFile(legacy); lerr == nil && len(lb) > 0 {
			var df settingsFile
			if yerr := yaml.Unmarshal(lb, &df); yerr == nil {
				s.applyFile(df, defaults)
				_ = os.Remove(legacy)
				return s.saveLocked()
			}
		}
		s.prefs = defaults
		s.configs = nil
		s.configID = ""
		return s.saveLocked()
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		s.prefs = defaults
		s.configs = nil
		s.configID = ""
		return s.saveLocked()
	}
	var df settingsFile
	if err := yaml.Unmarshal(b, &df); err != nil {
		return err
	}
	// If settings has no configs yet, merge legacy data.yaml once.
	if len(df.Configs) == 0 && len(df.Items) == 0 {
		legacy := filepath.Join(filepath.Dir(s.path), "data.yaml")
		if lb, lerr := os.ReadFile(legacy); lerr == nil && len(lb) > 0 {
			var old settingsFile
			if yerr := yaml.Unmarshal(lb, &old); yerr == nil {
				if len(old.Configs) > 0 {
					df.Configs = old.Configs
				} else if len(old.Items) > 0 {
					df.Configs = old.Items
				}
				if df.ConfigID == "" {
					if old.ConfigID != "" {
						df.ConfigID = old.ConfigID
					} else if old.ActiveID != "" {
						df.ConfigID = old.ActiveID
					}
				}
				_ = os.Remove(legacy)
			}
		}
	}
	s.applyFile(df, defaults)
	return s.saveLocked()
}

func (s *Store) applyFile(df settingsFile, defaults UIPrefs) {
	// migrate old keys
	if df.ConfigID == "" && df.ActiveID != "" {
		df.ConfigID = df.ActiveID
	}
	if len(df.Configs) == 0 && len(df.Items) > 0 {
		df.Configs = df.Items
	}

	s.prefs = UIPrefs{
		Mode:      df.Mode,
		LogLevel:  df.LogLevel,
		TunEnable: df.TunEnable,
	}
	if s.prefs.Mode == "" {
		s.prefs.Mode = defaults.Mode
	}
	if s.prefs.LogLevel == "" {
		s.prefs.LogLevel = defaults.LogLevel
	}

	s.configs = df.Configs
	if s.configs == nil {
		s.configs = []Config{}
	}
	s.normalizeLocked(df.ConfigID)
}

func (s *Store) normalizeLocked(preferID string) {
	if preferID == "" {
		for _, cfg := range s.configs {
			if cfg.LegacyActive || cfg.Enabled {
				preferID = cfg.ID
				break
			}
		}
	}
	found := false
	for i := range s.configs {
		if s.configs[i].Source == "" {
			if s.configs[i].URL != "" {
				s.configs[i].Source = "url"
			} else {
				s.configs[i].Source = "file"
			}
		}
		if preferID != "" && s.configs[i].ID == preferID {
			s.configs[i].Active = true
			found = true
		} else {
			s.configs[i].Active = false
		}
		s.configs[i].Enabled = false
		s.configs[i].LegacyActive = false
	}
	if found {
		s.configID = preferID
	} else {
		// no active selection is valid — kernel runs base shell via InstallEmpty
		s.configID = ""
	}
}

func (s *Store) saveLocked() error {
	df := settingsFile{
		Mode:      s.prefs.Mode,
		LogLevel:  s.prefs.LogLevel,
		TunEnable: s.prefs.TunEnable,
		ConfigID:  s.configID,
		Configs:   s.configs,
	}
	if df.Configs == nil {
		df.Configs = []Config{}
	}
	// keep configId in sync with Active flags
	df.ConfigID = ""
	for _, cfg := range s.configs {
		if cfg.Active {
			df.ConfigID = cfg.ID
			break
		}
	}
	s.configID = df.ConfigID

	b, err := yaml.Marshal(df)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Prefs() UIPrefs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prefs
}

func (s *Store) SetMode(mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prefs.Mode = mode
	return s.saveLocked()
}

func (s *Store) SetLogLevel(level string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prefs.LogLevel = level
	return s.saveLocked()
}

func (s *Store) SetTunEnable(enable bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prefs.TunEnable = enable
	return s.saveLocked()
}

func (s *Store) List() []Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Config, len(s.configs))
	copy(out, s.configs)
	return out
}

func (s *Store) Active() (Config, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cfg := range s.configs {
		if cfg.Active {
			return cfg, true
		}
	}
	return Config{}, false
}

func (s *Store) ActiveList() []Config {
	if cfg, ok := s.Active(); ok {
		return []Config{cfg}
	}
	return nil
}

func (s *Store) Add(name, url, source string, interval int) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		return Config{}, errors.New("name required")
	}
	if source == "" {
		if url != "" {
			source = "url"
		} else {
			source = "file"
		}
	}
	if source == "url" && url == "" {
		return Config{}, errors.New("url required for url source")
	}
	if interval < 0 {
		interval = 0
	}
	now := Now()
	cfg := Config{
		ID:        uuid.NewString(),
		Name:      name,
		URL:       url,
		Source:    source,
		Active:    false, // add only; activate is an explicit user action
		Interval:  interval,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.configs = append(s.configs, cfg)
	return cfg, s.saveLocked()
}

type ConfigPatch struct {
	Name     *string
	URL      *string
	Source   *string
	Interval *int
}

func (s *Store) Update(id string, p ConfigPatch) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.configs {
		if s.configs[i].ID != id {
			continue
		}
		if p.Name != nil {
			s.configs[i].Name = *p.Name
		}
		if p.URL != nil {
			s.configs[i].URL = *p.URL
		}
		if p.Source != nil {
			s.configs[i].Source = *p.Source
		}
		if p.Interval != nil {
			if *p.Interval < 0 {
				s.configs[i].Interval = 0
			} else {
				s.configs[i].Interval = *p.Interval
			}
		}
		s.configs[i].UpdatedAt = Now()
		if err := s.saveLocked(); err != nil {
			return Config{}, err
		}
		return s.configs[i], nil
	}
	return Config{}, errors.New("not found")
}

func (s *Store) SetActive(id string) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found *Config
	for i := range s.configs {
		if s.configs[i].ID == id {
			s.configs[i].Active = true
			s.configs[i].UpdatedAt = Now()
			found = &s.configs[i]
			s.configID = id
		} else {
			s.configs[i].Active = false
		}
	}
	if found == nil {
		return Config{}, errors.New("not found")
	}
	if err := s.saveLocked(); err != nil {
		return Config{}, err
	}
	return *found, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	wasActive := false
	idx := -1
	for i := range s.configs {
		if s.configs[i].ID == id {
			wasActive = s.configs[i].Active
			idx = i
			break
		}
	}
	if idx < 0 {
		return errors.New("not found")
	}
	s.configs = append(s.configs[:idx], s.configs[idx+1:]...)
	if wasActive {
		// deleting the current config returns to base shell, not the next entry
		for i := range s.configs {
			s.configs[i].Active = false
		}
		s.configID = ""
	} else if s.configID == id {
		s.configID = ""
	}
	return s.saveLocked()
}

func (s *Store) Get(id string) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cfg := range s.configs {
		if cfg.ID == id {
			return cfg, nil
		}
	}
	return Config{}, errors.New("not found")
}
