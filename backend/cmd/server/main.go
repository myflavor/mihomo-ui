package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/xin/mihomo-ui/internal/api"
	"github.com/xin/mihomo-ui/internal/configgen"
	"github.com/xin/mihomo-ui/internal/configsvc"
	"github.com/xin/mihomo-ui/internal/mihomo"
	"github.com/xin/mihomo-ui/internal/store"
)

// defaultUIPassword is what the README ships; worth shouting about if public.
const defaultUIPassword = "mihomo-ui"

// isLoopback reports a host-only address; an empty host (":7080") is not one.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// killedBySignal tells our own Stop() signal apart from a real crash.
func killedBySignal(err error) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return false
	}
	return ws.Signal() == syscall.SIGTERM || ws.Signal() == syscall.SIGKILL
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// installBootConfig rebuilds config.yaml each boot so no stale key survives,
// falling back to a bootable shell so a broken config still starts the kernel.
func installBootConfig(configPath string, active []store.Config, opts configgen.InstallOptions) error {
	if err := configgen.ApplyConfigs(configPath, active, opts); err != nil {
		log.Printf("install active config failed (will still try to start mihomo): %v", err)
		return configgen.InstallEmpty(configPath, opts)
	}
	return nil
}

// watchKernelExit tears the process down if mihomo dies. Done() is a broadcast,
// so the flag keeps our own shutdown from being logged as a crash.
func watchKernelExit(kernel *mihomo.Kernel, shuttingDown *atomic.Bool, sigCh chan<- os.Signal) {
	<-kernel.Done()
	err := kernel.Err()
	if shuttingDown.Load() {
		// Anything but our own signal means it died on its own; still worth logging.
		if err != nil && !killedBySignal(err) {
			log.Printf("mihomo exited during shutdown: %v", err)
		}
		return
	}
	if err != nil {
		log.Printf("mihomo exited: %v; shutting down", err)
	} else {
		log.Printf("mihomo exited; shutting down")
	}
	sigCh <- syscall.SIGTERM
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
	uiPassword := env("UI_PASSWORD", defaultUIPassword)
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

	// base ⊕ config ⊕ settings ⊕ secret → config.yaml; forces secret/controller.
	installOpts := configgen.InstallOptions{
		BasePath:  basePath,
		ConfigDir: configDir,
		Secret:    secret,
		UI:        configgen.UIStateFromPrefs(cfgStore.Prefs()),
	}
	if err := installBootConfig(configPath, cfgStore.ActiveList(), installOpts); err != nil {
		log.Fatal(err)
	}

	switch {
	case uiPassword == "":
		log.Printf("WARNING: UI_PASSWORD is empty — the panel API is completely open")
	case uiPassword == defaultUIPassword && !isLoopback(addr):
		log.Printf("WARNING: UI_PASSWORD is still the documented default and the panel")
		log.Printf("WARNING: listens on %s. Anyone who can reach that address can take", addr)
		log.Printf("WARNING: over the proxy. Set UI_PASSWORD to something private.")
	default:
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
		ConfigDir:  configDir,
		StaticDir:  staticDir,
		Config: &configsvc.Service{
			ConfigPath: configPath,
			BasePath:   basePath,
			ConfigDir:  configDir,
			Secret:     secret,
			Store:      cfgStore,
			Kernel:     client,
		},
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

	var shuttingDown atomic.Bool
	go watchKernelExit(kernel, &shuttingDown, sigCh)

	<-sigCh
	shuttingDown.Store(true)
	log.Printf("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = httpSrv.Shutdown(ctx)
	cancel()
	kernel.Stop()
}
