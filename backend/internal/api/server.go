package api

import (
	"net/http"
	"strings"

	"github.com/xin/mihomo-ui/internal/configsvc"
	"github.com/xin/mihomo-ui/internal/mihomo"
	"github.com/xin/mihomo-ui/internal/store"
	"github.com/xin/mihomo-ui/internal/web"
)

type Server struct {
	Mihomo     *mihomo.Client
	Store      *store.Store
	UIPassword string
	ConfigPath string
	ConfigDir  string

	// Config owns mihomo/config.yaml; handlers must never write it themselves.
	Config *configsvc.Service

	sessions *sessionStore
}

func (s *Server) Routes() http.Handler {
	s.sessions = newSessionStore()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/overview", s.handleOverview)
	mux.HandleFunc("/api/mode", s.handleMode)
	mux.HandleFunc("/api/tun", s.handleTun)
	mux.HandleFunc("/api/proxies", s.handleProxies)
	mux.HandleFunc("/api/proxies/select", s.handleSelect)
	mux.HandleFunc("/api/proxies/delay", s.handleDelay)
	mux.HandleFunc("/api/group/delay", s.handleGroupDelay)
	mux.HandleFunc("/api/config/list", s.handleConfigList)
	mux.HandleFunc("/api/config/apply", s.handleApply)
	mux.HandleFunc("/api/config/refresh", s.handleRefreshConfigs)
	mux.HandleFunc("/api/config/", s.handleConfigItem)
	mux.HandleFunc("/api/config", s.handleConfigCreate)
	mux.HandleFunc("/api/providers/update", s.handleUpdateProvider)
	mux.HandleFunc("/api/runtime", s.handleRuntime)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/log-level", s.handleLogLevel)
	mux.HandleFunc("/api/traffic", s.handleTraffic)
	mux.HandleFunc("/api/connections", s.handleConnections)

	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.handleLogout)
	mux.HandleFunc("/api/auth/check", s.handleAuthCheck)

	mux.Handle("/", web.Handler())
	// We serve the SPA ourselves, so everything is same-origin: no CORS headers.
	return s.withAuth(mux)
}

// withAuth protects /api/* except health + login. Static SPA is public; APIs need password.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		switch r.URL.Path {
		case "/api/health", "/api/login", "/api/logout", "/api/auth/check":
			next.ServeHTTP(w, r)
			return
		}
		if !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mihomo-ui"`)
			writeJSON(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
