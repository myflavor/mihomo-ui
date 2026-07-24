package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/xin/mihomo-ui/internal/configgen"
	"github.com/xin/mihomo-ui/internal/store"
)

func (s *Server) handleConfigList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errMethod)
		return
	}
	active, _ := s.Store.Active()
	writeJSON(w, 200, map[string]any{
		"configs": s.Store.List(),
		"active":  active,
	})
}

func (s *Server) handleConfigCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	// support JSON or multipart (file upload)
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		s.handleConfigUpload(w, r, "")
		return
	}
	var body struct {
		Name     string `json:"name"`
		URL      string `json:"url"`
		Source   string `json:"source"`
		Interval int    `json:"interval"`
		Content  string `json:"content"` // optional inline yaml
		Activate *bool  `json:"activate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, err)
		return
	}
	source := body.Source
	if source == "" {
		if body.Content != "" {
			source = "file"
		} else {
			source = "url"
		}
	}
	cfg, err := s.Store.Add(body.Name, body.URL, source, body.Interval)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	bres, berr := s.materializeConfigRaw(cfg, []byte(body.Content), source, body.URL)
	if berr != nil {
		_ = s.Store.Delete(cfg.ID)
		configgen.DeleteLocalConfig(s.ConfigDir, cfg.ID)
		writeJSON(w, 400, map[string]any{"error": berr.Error(), "detail": bres})
		return
	}
	// add only caches raw; do not switch active unless caller asks
	activate := body.Activate != nil && *body.Activate
	if activate {
		if _, err := s.Store.SetActive(cfg.ID); err != nil {
			writeErr(w, 500, err)
			return
		}
		cfg, _ = s.Store.Get(cfg.ID)
		res, err := s.installActiveAndReload(cfg)
		writeConfigApply(w, 201, cfg, res, err)
		return
	}
	writeJSON(w, 201, map[string]any{"config": cfg, "ok": true})
}

func (s *Server) handleConfigUpload(w http.ResponseWriter, r *http.Request, existingID string) {
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeErr(w, 400, err)
		return
	}
	name := r.FormValue("name")
	urlStr := r.FormValue("url")
	source := r.FormValue("source")
	interval := 0 // 0 = no auto update
	if v := r.FormValue("interval"); v != "" {
		interval = parseIntervalForm(v)
	}
	var content []byte
	if f, _, err := r.FormFile("file"); err == nil {
		defer f.Close()
		content, _ = io.ReadAll(f)
	}
	if content == nil && r.FormValue("content") != "" {
		content = []byte(r.FormValue("content"))
	}
	if source == "" {
		if len(content) > 0 {
			source = "file"
		} else {
			source = "url"
		}
	}
	if name == "" {
		writeJSON(w, 400, map[string]string{"error": "name required"})
		return
	}

	var cfg store.Config
	var err error
	if existingID == "" {
		cfg, err = s.Store.Add(name, urlStr, source, interval)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
	} else {
		p := store.ConfigPatch{Name: &name, URL: &urlStr, Source: &source, Interval: &interval}
		cfg, err = s.Store.Update(existingID, p)
		if err != nil {
			writeErr(w, 404, err)
			return
		}
	}
	if len(content) > 0 {
		src := "file"
		cfg, _ = s.Store.Update(cfg.ID, store.ConfigPatch{Source: &src})
		source = "file"
	}
	bres, berr := s.materializeConfigRaw(cfg, content, source, urlStr)
	if berr != nil {
		// create path: roll back store entry + any partial local file (match JSON create)
		if existingID == "" {
			_ = s.Store.Delete(cfg.ID)
			configgen.DeleteLocalConfig(s.ConfigDir, cfg.ID)
		}
		writeJSON(w, 400, map[string]any{"error": berr.Error(), "detail": bres})
		return
	}

	// activate only when explicitly requested (create never auto-switches)
	if r.FormValue("activate") == "1" || r.FormValue("activate") == "true" {
		cfg, _ = s.Store.SetActive(cfg.ID)
		res, err := s.installActiveAndReload(cfg)
		writeConfigApply(w, 200, cfg, res, err)
		return
	}
	writeJSON(w, 200, map[string]any{"config": cfg, "ok": true})
}

func (s *Server) handleConfigItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/config/")
	rest = strings.Trim(rest, "/")
	if rest == "" || rest == "apply" || rest == "refresh" {
		http.NotFound(w, r)
		return
	}
	// /api/config/{id}/activate
	// /api/config/{id}/upload
	parts := strings.Split(rest, "/")
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	if action == "activate" && r.Method == http.MethodPost {
		// install from local raw — no re-download on switch
		cfg, err := s.Store.SetActive(id)
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		res, err := s.installActiveAndReload(cfg)
		writeConfigApply(w, 200, cfg, res, err)
		return
	}

	// /api/config/{id}/refresh — re-download raw for this URL config
	if action == "refresh" && r.Method == http.MethodPost {
		cfg, err := s.Store.Get(id)
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		if cfg.Source == "file" || cfg.URL == "" {
			writeJSON(w, 400, map[string]string{"error": "本地文件无需更新"})
			return
		}
		res, err := s.refreshConfigAndMaybeInstall(cfg, true)
		if err != nil {
			writeJSON(w, 200, map[string]any{
				"config": cfg,
				"ok":     false,
				"error":  err.Error(),
				"detail": res,
			})
			return
		}
		// touch updatedAt
		cfg, _ = s.Store.Update(id, store.ConfigPatch{})
		if cfg.Active {
			errs := s.updateAllProviders(r.Context())
			if res != nil {
				errs = append(errs, res.Failed...)
			}
			writeJSON(w, 200, map[string]any{
				"config": cfg,
				"ok":     len(errs) == 0,
				"detail": res,
				"errors": errs,
			})
			return
		}
		writeJSON(w, 200, map[string]any{"config": cfg, "ok": true, "detail": res})
		return
	}

	if action == "upload" && r.Method == http.MethodPost {
		s.handleConfigUpload(w, r, id)
		return
	}

	if action == "raw" {
		s.handleConfigRaw(w, r, id)
		return
	}

	if action != "" {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		if err := s.Store.Delete(id); err != nil {
			writeErr(w, 404, err)
			return
		}
		configgen.DeleteLocalConfig(s.ConfigDir, id)
		// re-apply new active (if any)
		res, err := s.applyAndReload(false)
		if err != nil {
			writeJSON(w, 200, map[string]any{"ok": "1", "apply": map[string]any{"ok": "0", "error": err.Error(), "detail": res}})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": "1", "apply": map[string]any{"ok": "1", "detail": res}})
	case http.MethodPut:
		ct := r.Header.Get("Content-Type")
		if strings.HasPrefix(ct, "multipart/form-data") {
			s.handleConfigUpload(w, r, id)
			return
		}
		var body struct {
			Name     *string `json:"name"`
			URL      *string `json:"url"`
			Source   *string `json:"source"`
			Interval *int    `json:"interval"`
			Content  *string `json:"content"`
			Activate *bool   `json:"activate"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, 400, err)
			return
		}
		cfg, err := s.Store.Update(id, store.ConfigPatch{
			Name: body.Name, URL: body.URL, Source: body.Source, Interval: body.Interval,
		})
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		if body.Content != nil {
			if err := configgen.SaveLocalConfig(s.ConfigDir, id, []byte(*body.Content)); err != nil {
				writeErr(w, 400, err)
				return
			}
			// do NOT force source=file — editing raw of a URL cfg keeps source=url
		}
		// re-apply only if this is the active one, or activate requested
		if body.Activate != nil && *body.Activate {
			cfg, _ = s.Store.SetActive(id)
		}

		// Always re-run the full pipeline on edit:
		// - URL cfg: re-download raw
		// - file/raw content: reinstall from saved bytes
		// - if active: install + hot reload
		// Don't try to guess whether remote content changed.
		forceRefresh := cfg.Source != "file" && cfg.URL != "" && body.Content == nil
		res, err := s.refreshConfigAndMaybeInstall(cfg, forceRefresh)
		writeConfigApply(w, 200, cfg, res, err)
	default:
		writeErr(w, 405, errMethod)
	}
}

func (s *Server) handleConfigRaw(w http.ResponseWriter, r *http.Request, id string) {
	cfg, err := s.Store.Get(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	path := configgen.LocalConfigPath(s.ConfigDir, id)
	switch r.Method {
	case http.MethodGet:
		raw, err := configgen.ReadLocalConfigRaw(s.ConfigDir, id)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "尚未缓存原始配置，请先更新或重新添加", "path": path})
			return
		}
		writeJSON(w, 200, map[string]any{
			"id":      cfg.ID,
			"name":    cfg.Name,
			"source":  cfg.Source,
			"path":    path,
			"content": string(raw),
			"active":  cfg.Active,
		})
	case http.MethodPut, http.MethodPost:
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			// also accept raw text body
			b, err2 := io.ReadAll(r.Body)
			if err2 != nil {
				writeErr(w, 400, err)
				return
			}
			body.Content = string(b)
		}
		if strings.TrimSpace(body.Content) == "" {
			writeJSON(w, 400, map[string]string{"error": "content required"})
			return
		}
		if err := configgen.SaveLocalConfig(s.ConfigDir, id, []byte(body.Content)); err != nil {
			writeErr(w, 400, err)
			return
		}
		// always reinstall from edited raw when active
		res, err := s.refreshConfigAndMaybeInstall(cfg, false)
		if err != nil {
			writeJSON(w, 200, map[string]any{
				"ok":      "0",
				"path":    path,
				"error":   err.Error(),
				"detail":  res,
				"applied": cfg.Active,
			})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": "1", "path": path, "applied": cfg.Active, "detail": res})
	default:
		writeErr(w, 405, errMethod)
	}
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	res, err := s.applyAndReload(false)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": "1", "config-path": s.ConfigPath, "detail": res})
}

func (s *Server) handleRefreshConfigs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}

	// Refresh ALL url configs (re-download raw). File sources are skipped.
	// Then install the current active config and update providers.
	// Downloads run under applyMu so they don't race install/reload.
	result := &configgen.ApplyResult{}
	var errs []string
	refreshed := 0
	skipped := 0

	_ = s.withApplyLock(func() error {
		for _, cfg := range s.Store.List() {
			if cfg.Source == "file" || cfg.URL == "" {
				skipped++
				continue
			}
			br, e := configgen.EnsureConfig(s.ConfigDir, cfg, true)
			if br != nil {
				result.Warnings = append(result.Warnings, br.Warnings...)
				result.Failed = append(result.Failed, br.Failed...)
			}
			if e != nil {
				errs = append(errs, cfg.Name+": "+e.Error())
				continue
			}
			// touch updatedAt
			_, _ = s.Store.Update(cfg.ID, store.ConfigPatch{})
			refreshed++
			result.OK++
		}
		return nil
	})

	// Install current active (from refreshed raw if any; file active still reinstalls).
	ir, err := s.applyAndReload(false)
	if ir != nil {
		result.Failed = append(result.Failed, ir.Failed...)
		result.Warnings = append(result.Warnings, ir.Warnings...)
		if ir.OK > 0 {
			result.OK += ir.OK
		}
	}
	if err != nil {
		errs = append(errs, err.Error())
	}

	errs = append(errs, s.updateAllProviders(r.Context())...)
	if result != nil {
		errs = append(errs, result.Failed...)
	}

	writeJSON(w, 200, map[string]any{
		"ok":          len(errs) == 0,
		"config-path": s.ConfigPath,
		"refreshed":   refreshed,
		"skipped":     skipped,
		"detail":      result,
		"errors":      errs,
	})
}

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errMethod)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Name == "" {
		errs := s.updateAllProviders(r.Context())
		writeJSON(w, 200, map[string]any{"ok": len(errs) == 0, "errors": errs})
		return
	}
	if err := s.Mihomo.UpdateProvider(r.Context(), body.Name); err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "1"})
}
