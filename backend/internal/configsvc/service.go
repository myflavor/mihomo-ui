// Package configsvc is the sole writer of mihomo/config.yaml: it serializes
// writes internally so callers cannot forget a lock, and keeps slow downloads
// out of the critical section so a refresh never stalls a UI toggle.
package configsvc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/xin/mihomo-ui/internal/configgen"
	"github.com/xin/mihomo-ui/internal/store"
)

// reloadTimeout bounds a hot-reload; not the request context, since the file is
// already on disk and aborting would desync the kernel from it.
const reloadTimeout = 90 * time.Second

// ErrNoSuchConfig lets callers answer 404 rather than report an install failure.
var ErrNoSuchConfig = errors.New("config not found")

// Reloader is the slice of the mihomo client this package needs.
type Reloader interface {
	ReloadConfig(ctx context.Context, path string) error
}

// Service is the sole writer of mihomo/config.yaml.
type Service struct {
	ConfigPath   string // mihomo/config.yaml
	OverridePath string // ui/override.yaml
	ConfigDir    string // ui/config
	Secret       string // kernel API credential, generated per boot
	KernelAPI    string // host:port the kernel's control API is pinned to
	Store        *store.Store
	Kernel       Reloader

	mu sync.Mutex // held only for merge + write + reload, never across network IO
}

func (s *Service) installOpts() configgen.InstallOptions {
	return configgen.InstallOptions{
		OverridePath: s.OverridePath,
		ConfigDir:    s.ConfigDir,
		Secret:       s.Secret,
		KernelAPI:    s.KernelAPI,
		UI:           configgen.UIStateFromPrefs(s.Store.Prefs()),
	}
}

func (s *Service) reload() error {
	ctx, cancel := context.WithTimeout(context.Background(), reloadTimeout)
	defer cancel()
	return s.Kernel.ReloadConfig(ctx, s.ConfigPath)
}

// installLocked merges config ⊕ override ⊕ settings ⊕ forced and reloads; needs s.mu.
func (s *Service) installLocked(cfg store.Config) (*configgen.ApplyResult, error) {
	res, err := configgen.InstallActive(s.ConfigPath, cfg, s.installOpts())
	if err != nil {
		return res, err
	}
	return res, s.reload()
}

// mergeResult folds an install result into a preceding download result.
func mergeResult(res, install *configgen.ApplyResult) *configgen.ApplyResult {
	if install == nil {
		return res
	}
	if res == nil {
		return install
	}
	res.OK = install.OK
	res.Failed = append(res.Failed, install.Failed...)
	res.Warnings = append(res.Warnings, install.Warnings...)
	return res
}

// EnsureRaw writes ui/config/<id>.yaml, unlocked. The post-write delete check
// drops the file if the config vanished mid-download, so no orphan keeps the
// subscription's credentials; checking before the write would miss that race.
func (s *Service) EnsureRaw(cfg store.Config, forceRefresh bool) (*configgen.ApplyResult, error) {
	res, err := configgen.EnsureConfig(s.ConfigDir, cfg, forceRefresh)
	s.dropIfDeleted(cfg.ID)
	return res, err
}

// SaveRaw stores inline content as a config's raw file. No lock, same reason.
func (s *Service) SaveRaw(cfg store.Config, content []byte) error {
	err := configgen.SaveLocalConfig(s.ConfigDir, cfg.ID, content)
	s.dropIfDeleted(cfg.ID)
	return err
}

// FetchRaw downloads a URL config's raw file. No lock, same reason.
func (s *Service) FetchRaw(cfg store.Config) error {
	_, err := configgen.FetchAndSaveConfig(s.ConfigDir, cfg)
	s.dropIfDeleted(cfg.ID)
	return err
}

// DeleteRaw removes a config's raw file, so ui/config/ has exactly one owner.
func (s *Service) DeleteRaw(id string) {
	configgen.DeleteLocalConfig(s.ConfigDir, id)
}

func (s *Service) dropIfDeleted(id string) {
	if _, err := s.Store.Get(id); err != nil {
		configgen.DeleteLocalConfig(s.ConfigDir, id)
	}
}

// ApplyActive installs the store's active config, or a bootable empty shell.
func (s *Service) ApplyActive() (*configgen.ApplyResult, error) {
	// Fetch outside the lock, for the same reasons as Activate.
	if active, ok := s.Store.Active(); ok {
		if _, err := s.EnsureRaw(active, false); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := configgen.ApplyConfigsDetailed(s.ConfigPath, s.Store.ActiveList(), false, s.installOpts())
	if err != nil {
		return res, err
	}
	return res, s.reload()
}

// Activate flips the active flag and installs it in one lock hold; splitting
// them lets concurrent activations desync settings.yaml from config.yaml.
// forceRefresh re-downloads the raw file first, outside the lock.
func (s *Service) Activate(id string, forceRefresh bool) (store.Config, *configgen.ApplyResult, error) {
	cur, err := s.Store.Get(id)
	if err != nil {
		return store.Config{}, nil, fmt.Errorf("%w: %v", ErrNoSuchConfig, err)
	}
	// Before the lock for two reasons: installing would otherwise fetch a missing
	// file while holding it and stall the panel, and this parse is the only check
	// standing between an unusable config and SetActive marking it active.
	pre, err := s.EnsureRaw(cur, forceRefresh)
	if err != nil {
		return store.Config{}, pre, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	previous, hadPrevious := s.Store.Active()
	cfg, err := s.Store.SetActive(id)
	if err != nil {
		return cfg, pre, fmt.Errorf("%w: %v", ErrNoSuchConfig, err)
	}
	res, err := s.installLocked(cfg)
	if err != nil {
		// The kernel refused this config — it is still running the old one, so
		// leaving the flag flipped would have the panel and settings.yaml claim a
		// config that was never loaded. Put the pointer back where it was.
		s.restoreActive(previous, hadPrevious)
		return previous, mergeResult(pre, res), err
	}
	return cfg, mergeResult(pre, res), err
}

// restoreActive undoes a SetActive after the install it was paired with failed.
// A failure here is not worth surfacing over the install error the caller is
// already returning, but it does mean the two views have diverged, so say so.
func (s *Service) restoreActive(previous store.Config, had bool) {
	var err error
	if had {
		_, err = s.Store.SetActive(previous.ID)
	} else {
		err = s.Store.ClearActive()
	}
	if err != nil {
		log.Printf("configsvc: could not restore the previous active config: %v", err)
	}
}

// UpdateMeta changes a config's stored fields and, for a subscription, pulls the
// new URL before any of it is committed. Downloading first is what makes an edit
// atomic: a bad URL leaves the entry exactly as it was instead of persisting a
// name and address whose content was never fetched.
func (s *Service) UpdateMeta(id string, patch store.ConfigPatch) (store.Config, *configgen.ApplyResult, error) {
	cur, err := s.Store.Get(id)
	if err != nil {
		return store.Config{}, nil, fmt.Errorf("%w: %v", ErrNoSuchConfig, err)
	}

	// Probe with the patch applied but not yet saved, so the fetch uses the new
	// URL while the store still holds the old one.
	probe := cur
	if patch.URL != nil {
		probe.URL = *patch.URL
	}
	if patch.Source != nil {
		probe.Source = *patch.Source
	}
	var res *configgen.ApplyResult
	if probe.Source != "file" && probe.URL != "" {
		if res, err = s.EnsureRaw(probe, true); err != nil {
			return cur, res, err
		}
	}

	cfg, err := s.Store.Update(id, patch)
	if err != nil {
		return cur, res, fmt.Errorf("%w: %v", ErrNoSuchConfig, err)
	}
	if !cfg.Active {
		return cfg, res, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	install, err := s.installLocked(cfg)
	return cfg, mergeResult(res, install), err
}

// Refresh re-downloads cfg's raw file and reinstalls it if still active.
func (s *Service) Refresh(cfg store.Config, forceRefresh bool) (*configgen.ApplyResult, error) {
	res, err := s.EnsureRaw(cfg, forceRefresh)
	if err != nil {
		return res, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Decide from the store, never from cfg: that snapshot predates a download
	// that may have taken minutes, during which this config could have been
	// activated (install it) or switched away from (leave the kernel alone).
	cur, err := s.Store.Get(cfg.ID)
	if err != nil || !cur.Active {
		return res, nil
	}
	install, err := s.installLocked(cur)
	return mergeResult(res, install), err
}

// PatchRuntime persists mode/log-level/tun so they survive a reload or switch.
func (s *Service) PatchRuntime(patch map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return configgen.PatchYAMLFile(s.ConfigPath, patch)
}

// WriteRaw replaces config.yaml from the raw editor. The two errors are split
// because a failed reload still leaves valid content on disk.
func (s *Service) WriteRaw(content []byte, reload bool) (writeErr, reloadErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := configgen.WriteConfigFile(s.ConfigPath, content); err != nil {
		return err, nil
	}
	if !reload {
		return nil, nil
	}
	return nil, s.reload()
}
