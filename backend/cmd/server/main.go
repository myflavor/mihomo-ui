package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

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
	// DATA_HOME/
	//   mihomo/          kernel home (mihomo -d)
	//   ui/
	//     base.yaml      merge base (seeded from embed)
	//     settings.yaml  panel switches + configs list
	//     config/        config raw YAML
	dataHome := env("DATA_HOME", "/data/mihomo-ui")
	mihomoDir := filepath.Join(dataHome, "mihomo")
	uiDir := filepath.Join(dataHome, "ui")
	addr := env("UI_ADDR", ":7080")
	mihomoURL := env("MIHOMO_API", "http://127.0.0.1:9090")
	secret := env("MIHOMO_SECRET", "mihomo")
	uiPassword := env("UI_PASSWORD", "mihomo-ui")
	mihomoBin := env("MIHOMO_BIN", "/mihomo")
	configPath := filepath.Join(mihomoDir, "config.yaml")
	basePath := filepath.Join(uiDir, "base.yaml")
	configDir := filepath.Join(uiDir, "config")
	staticDir := env("STATIC_DIR", "/app/web")

	for _, d := range []string{mihomoDir, uiDir, configDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Fatal(err)
		}
	}

	// Seed ui/base.yaml once from embedded template (never overwrite user edits).
	if err := configgen.EnsureBase(basePath); err != nil {
		log.Fatal(err)
	}

	def := configgen.DefaultUIStateFromBase(basePath)
	cfgStore, err := store.New(filepath.Join(uiDir, "settings.yaml"), store.UIPrefs{
		Mode:      def.Mode,
		LogLevel:  def.LogLevel,
		TunEnable: def.TunEnable,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Every boot: base ⊕ active config ⊕ settings ⊕ secret → mihomo/config.yaml
	// (forces secret / external-controller; no stale leftover keys from old runs).
	installOpts := configgen.InstallOptions{
		BasePath:  basePath,
		ConfigDir: configDir,
		Secret:    secret,
		UI:        configgen.UIStateFromPrefs(cfgStore.Prefs()),
	}
	if err := configgen.ApplyConfigs(configPath, cfgStore.ActiveList(), installOpts); err != nil {
		log.Printf("install active config failed (will still try to start mihomo): %v", err)
		// Fall back to minimal bootable shell so kernel can start.
		if err2 := configgen.InstallEmpty(configPath, installOpts); err2 != nil {
			log.Fatal(err2)
		}
	}

	if uiPassword == "" {
		log.Printf("WARNING: UI_PASSWORD is empty — panel API is open")
	} else {
		log.Printf("UI password auth enabled")
	}

	client := mihomo.NewClient(mihomoURL, secret)

	// Start mihomo kernel as child process.
	kernel := &mihomo.Kernel{Bin: mihomoBin, Home: mihomoDir}
	if err := kernel.Start(); err != nil {
		log.Fatal(err)
	}
	if err := kernel.WaitReady(client, 15*time.Second); err != nil {
		kernel.Stop()
		log.Fatal(err)
	}

	srv := &api.Server{
		Mihomo:     client,
		MihomoURL:  mihomoURL,
		Secret:     secret,
		UIPassword: uiPassword,
		Store:      cfgStore,
		ConfigPath: configPath,
		BasePath:   basePath,
		ConfigDir:  configDir,
		StaticDir:  staticDir,
	}

	httpSrv := &http.Server{Addr: addr, Handler: srv.Routes()}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("mihomo-ui listening on %s (data=%s api=%s bin=%s)", addr, dataHome, mihomoURL, mihomoBin)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("http server error: %v", err)
			sigCh <- syscall.SIGTERM
		}
	}()

	// If mihomo dies, tear down the whole process.
	go func() {
		err := <-kernel.Done()
		if err != nil {
			log.Printf("mihomo exited: %v; shutting down", err)
		} else {
			log.Printf("mihomo exited; shutting down")
		}
		sigCh <- syscall.SIGTERM
	}()

	<-sigCh
	log.Printf("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = httpSrv.Shutdown(ctx)
	cancel()
	kernel.Stop()
}
