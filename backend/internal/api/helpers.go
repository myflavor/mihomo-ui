package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xin/mihomo-ui/internal/configgen"
	"github.com/xin/mihomo-ui/internal/store"
)

const applyTimeout = 90 * time.Second

func (s *Server) withApplyLock(fn func() error) error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	return fn()
}

func applyContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), applyTimeout)
}

// applyAndReload installs the active config into config.yaml and hot-reloads.
// forceRefresh re-downloads URL raw then installs.
func (s *Server) applyAndReload(forceRefresh bool) (*configgen.ApplyResult, error) {
	var res *configgen.ApplyResult
	err := s.withApplyLock(func() error {
		var e error
		res, e = configgen.ApplyConfigsDetailed(s.ConfigPath, s.Store.ActiveList(), forceRefresh, s.installOpts())
		if e != nil {
			return e
		}
		ctx, cancel := applyContext()
		defer cancel()
		return s.Mihomo.ReloadConfig(ctx, s.ConfigPath)
	})
	return res, err
}

// installActiveAndReload merges base ⊕ raw cfg ⊕ settings ⊕ secret and hot-reloads.
func (s *Server) installActiveAndReload(cfg store.Config) (*configgen.ApplyResult, error) {
	var res *configgen.ApplyResult
	err := s.withApplyLock(func() error {
		var e error
		res, e = configgen.InstallActive(s.ConfigPath, cfg, s.installOpts())
		if e != nil {
			return e
		}
		ctx, cancel := applyContext()
		defer cancel()
		return s.Mihomo.ReloadConfig(ctx, s.ConfigPath)
	})
	return res, err
}

// refreshConfigAndMaybeInstall ensures raw (optional re-download); if active, install+reload.
// Whole ensure → install → reload path is serialized under applyMu.
func (s *Server) refreshConfigAndMaybeInstall(cfg store.Config, forceRefresh bool) (*configgen.ApplyResult, error) {
	var res *configgen.ApplyResult
	err := s.withApplyLock(func() error {
		var e error
		res, e = configgen.EnsureConfig(s.ConfigDir, cfg, forceRefresh)
		if e != nil {
			return e
		}
		if !cfg.Active {
			return nil
		}
		ir, e := configgen.InstallActive(s.ConfigPath, cfg, s.installOpts())
		if ir != nil {
			if res == nil {
				res = ir
			} else {
				res.OK = ir.OK
				res.Failed = append(res.Failed, ir.Failed...)
				res.Warnings = append(res.Warnings, ir.Warnings...)
			}
		}
		if e != nil {
			return e
		}
		ctx, cancel := applyContext()
		defer cancel()
		return s.Mihomo.ReloadConfig(ctx, s.ConfigPath)
	})
	return res, err
}

// updateAllProviders refreshes non-Compatible mihomo proxy providers.
func (s *Server) updateAllProviders(ctx context.Context) []string {
	out, err := s.Mihomo.Providers(ctx)
	if err != nil {
		return []string{err.Error()}
	}
	providers, _ := out["providers"].(map[string]any)
	var errs []string
	for name, raw := range providers {
		m, _ := raw.(map[string]any)
		if vt, _ := m["vehicleType"].(string); vt == "Compatible" {
			continue
		}
		if uerr := s.Mihomo.UpdateProvider(ctx, name); uerr != nil {
			errs = append(errs, name+": "+uerr.Error())
		}
	}
	return errs
}

func writeConfigApply(w http.ResponseWriter, code int, cfg store.Config, res *configgen.ApplyResult, err error) {
	if err != nil {
		writeJSON(w, code, map[string]any{
			"config": cfg,
			"apply":  map[string]any{"ok": "0", "error": err.Error(), "detail": res},
		})
		return
	}
	writeJSON(w, code, map[string]any{
		"config": cfg,
		"apply":  map[string]any{"ok": "1", "detail": res},
	})
}

func parseIntervalForm(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// materializeConfigRaw ensures raw YAML is on disk for cfg.
// Non-empty content is saved as local file; otherwise URL sources are fetched.
// Disk write/download is serialized under applyMu so concurrent create/refresh
// cannot interleave with install+reload.
func (s *Server) materializeConfigRaw(cfg store.Config, content []byte, source, urlStr string) (*configgen.ApplyResult, error) {
	var res *configgen.ApplyResult
	err := s.withApplyLock(func() error {
		if len(content) > 0 {
			return configgen.SaveLocalConfig(s.ConfigDir, cfg.ID, content)
		}
		if source != "file" && urlStr != "" {
			if _, e := configgen.FetchAndSaveConfig(s.ConfigDir, cfg); e != nil {
				return e
			}
			// Fetch already wrote the file; only ensure when something still missing.
			if configgen.HasLocalConfig(s.ConfigDir, cfg.ID) {
				return nil
			}
		}
		var e error
		res, e = configgen.EnsureConfig(s.ConfigDir, cfg, false)
		return e
	})
	return res, err
}
