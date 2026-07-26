package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/xin/mihomo-ui/internal/configgen"
	"github.com/xin/mihomo-ui/internal/store"
)

// errMethod is the body every handler returns for a verb it does not implement.
var errMethod = errors.New("method not allowed")

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// isWriteMethod accepts the setter verbs interchangeably; the frontend uses POST.
func isWriteMethod(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	}
	return false
}

// contentBody is what both YAML editors submit; only the kernel one sets Reload.
type contentBody struct {
	Content string `json:"content"`
	Reload  *bool  `json:"reload"`
}

// readContentBody takes the JSON envelope or a bare YAML document. It reads the
// body whole first, since a Decoder buffers ahead and leaves nothing to re-read.
func readContentBody(r *http.Request) (contentBody, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return contentBody{}, err
	}
	var body contentBody
	if json.Unmarshal(raw, &body) != nil {
		return contentBody{Content: string(raw)}, nil
	}
	return body, nil
}

// materializeConfigRaw puts raw YAML on disk, inline content winning over a
// fetch. Runs outside the config.yaml lock — see configsvc.
func (s *Server) materializeConfigRaw(cfg store.Config, content []byte, source, urlStr string) (*configgen.ApplyResult, error) {
	if len(content) > 0 {
		return nil, s.Config.SaveRaw(cfg, content)
	}
	if source != "file" && urlStr != "" {
		if err := s.Config.FetchRaw(cfg); err != nil {
			return nil, err
		}
		// Fetch already wrote the file; only ensure when something is still missing.
		if configgen.HasLocalConfig(s.ConfigDir, cfg.ID) {
			return nil, nil
		}
	}
	return s.Config.EnsureRaw(cfg, false)
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

// parseIntervalForm reads minutes; absent, malformed and negative all mean off.
func parseIntervalForm(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
