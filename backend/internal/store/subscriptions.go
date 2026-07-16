package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Source: url | file
// Only one subscription is Active at a time — that is the one applied to the kernel.

type Subscription struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url,omitempty"`
	Source    string    `json:"source"` // url | file
	Active    bool      `json:"active"`
	Interval  int       `json:"interval"` // seconds; 0 = no auto for file
	UpdatedAt time.Time `json:"updatedAt"`
	CreatedAt time.Time `json:"createdAt"`

	// deprecated, kept for migration from older JSON
	Enabled bool `json:"enabled,omitempty"`
}

type dataFile struct {
	ActiveID string         `json:"activeId"`
	Items    []Subscription `json:"items"`
}

type Store struct {
	path string
	mu   sync.Mutex
	subs []Subscription
}

func New(path string) (*Store, error) {
	s := &Store{path: path}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.subs = []Subscription{}
			return s.saveLocked()
		}
		return err
	}
	if len(b) == 0 {
		s.subs = []Subscription{}
		return nil
	}
	// try new format first
	var df dataFile
	if err := json.Unmarshal(b, &df); err == nil && (df.Items != nil || df.ActiveID != "") {
		s.subs = df.Items
		s.normalizeLocked(df.ActiveID)
		return s.saveLocked()
	}
	// legacy: bare array with enabled flags
	var legacy []Subscription
	if err := json.Unmarshal(b, &legacy); err != nil {
		return err
	}
	s.subs = legacy
	// migrate enabled → single active (first enabled, else first)
	activeID := ""
	for _, sub := range s.subs {
		if sub.Enabled || sub.Active {
			activeID = sub.ID
			break
		}
	}
	s.normalizeLocked(activeID)
	return s.saveLocked()
}

func (s *Store) normalizeLocked(preferID string) {
	if len(s.subs) == 0 {
		return
	}
	found := false
	for i := range s.subs {
		if s.subs[i].Source == "" {
			if s.subs[i].URL != "" {
				s.subs[i].Source = "url"
			} else {
				s.subs[i].Source = "file"
			}
		}
		if preferID != "" && s.subs[i].ID == preferID {
			s.subs[i].Active = true
			found = true
		} else {
			s.subs[i].Active = false
		}
		s.subs[i].Enabled = false // strip legacy
	}
	if !found {
		// pick first as active
		s.subs[0].Active = true
	}
}

func (s *Store) saveLocked() error {
	df := dataFile{Items: s.subs}
	for _, sub := range s.subs {
		if sub.Active {
			df.ActiveID = sub.ID
			break
		}
	}
	b, err := json.MarshalIndent(df, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) List() []Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Subscription, len(s.subs))
	copy(out, s.subs)
	return out
}

func (s *Store) Active() (Subscription, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sub := range s.subs {
		if sub.Active {
			return sub, true
		}
	}
	return Subscription{}, false
}

// ActiveList returns the single active subscription as a slice (0 or 1).
// Used by configgen which iterates "enabled" subscriptions.
func (s *Store) ActiveList() []Subscription {
	if sub, ok := s.Active(); ok {
		return []Subscription{sub}
	}
	return nil
}

func (s *Store) Add(name, url, source string, interval int) (Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		return Subscription{}, errors.New("name required")
	}
	if source == "" {
		if url != "" {
			source = "url"
		} else {
			source = "file"
		}
	}
	if source == "url" && url == "" {
		return Subscription{}, errors.New("url required for url source")
	}
	// interval <= 0 means no auto update
	if interval < 0 {
		interval = 0
	}
	now := time.Now()
	sub := Subscription{
		ID:        uuid.NewString(),
		Name:      name,
		URL:       url,
		Source:    source,
		Active:    len(s.subs) == 0, // first becomes active
		Interval:  interval,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.subs = append(s.subs, sub)
	return sub, s.saveLocked()
}

type SubPatch struct {
	Name     *string
	URL      *string
	Source   *string
	Interval *int
}

func (s *Store) Update(id string, p SubPatch) (Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.subs {
		if s.subs[i].ID != id {
			continue
		}
		if p.Name != nil {
			s.subs[i].Name = *p.Name
		}
		if p.URL != nil {
			s.subs[i].URL = *p.URL
		}
		if p.Source != nil {
			s.subs[i].Source = *p.Source
		}
		if p.Interval != nil {
			// allow 0 = disable auto update
			if *p.Interval < 0 {
				s.subs[i].Interval = 0
			} else {
				s.subs[i].Interval = *p.Interval
			}
		}
		s.subs[i].UpdatedAt = time.Now()
		if err := s.saveLocked(); err != nil {
			return Subscription{}, err
		}
		return s.subs[i], nil
	}
	return Subscription{}, errors.New("not found")
}

func (s *Store) SetActive(id string) (Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found *Subscription
	for i := range s.subs {
		if s.subs[i].ID == id {
			s.subs[i].Active = true
			s.subs[i].UpdatedAt = time.Now()
			found = &s.subs[i]
		} else {
			s.subs[i].Active = false
		}
	}
	if found == nil {
		return Subscription{}, errors.New("not found")
	}
	if err := s.saveLocked(); err != nil {
		return Subscription{}, err
	}
	return *found, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	wasActive := false
	idx := -1
	for i := range s.subs {
		if s.subs[i].ID == id {
			wasActive = s.subs[i].Active
			idx = i
			break
		}
	}
	if idx < 0 {
		return errors.New("not found")
	}
	s.subs = append(s.subs[:idx], s.subs[idx+1:]...)
	if wasActive && len(s.subs) > 0 {
		s.subs[0].Active = true
	}
	return s.saveLocked()
}

func (s *Store) Get(id string) (Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sub := range s.subs {
		if sub.ID == id {
			return sub, nil
		}
	}
	return Subscription{}, errors.New("not found")
}

// Enabled kept for any leftover callers — maps to ActiveList.
func (s *Store) Enabled() []Subscription {
	return s.ActiveList()
}
