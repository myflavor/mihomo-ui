package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/xin/mihomo-ui/internal/api"
	"github.com/xin/mihomo-ui/internal/configgen"
	"github.com/xin/mihomo-ui/internal/mihomo"
	"github.com/xin/mihomo-ui/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	// Single data home for kernel + panel.
	dataHome := env("DATA_HOME", "/data/mihomo-ui")
	addr := env("UI_ADDR", ":8080")
	mihomoURL := env("MIHOMO_API", "http://127.0.0.1:9090")
	secret := env("MIHOMO_SECRET", "mihomo")
	uiPassword := env("UI_PASSWORD", "mihomo-ui")
	configPath := filepath.Join(dataHome, "config.yaml")
	basePath := filepath.Join(dataHome, "base.yaml")
	staticDir := env("STATIC_DIR", "/app/web")

	if err := os.MkdirAll(dataHome, 0o755); err != nil {
		log.Fatal(err)
	}
	subStore, err := store.New(filepath.Join(dataHome, "subscriptions.json"))
	if err != nil {
		log.Fatal(err)
	}
	uiState, err := configgen.NewUIStateStore(filepath.Join(dataHome, "ui-state.json"))
	if err != nil {
		log.Fatal(err)
	}

	if uiPassword == "" {
		log.Printf("WARNING: UI_PASSWORD is empty — panel API is open")
	} else {
		log.Printf("UI password auth enabled")
	}

	srv := &api.Server{
		Mihomo:     mihomo.NewClient(mihomoURL, secret),
		MihomoURL:  mihomoURL,
		Secret:     secret,
		UIPassword: uiPassword,
		Store:      subStore,
		UIState:    uiState,
		ConfigPath: configPath,
		BasePath:   basePath,
		StaticDir:  staticDir,
	}

	log.Printf("mihomo-ui listening on %s (data=%s api=%s)", addr, dataHome, mihomoURL)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}
