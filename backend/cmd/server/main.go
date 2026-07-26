package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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

// newKernelSecret mints the credential the panel uses to talk to its own
// kernel. It is generated per boot rather than configured: the control API is
// pinned to loopback and nothing outside this process ever needs the value, so
// a fixed default would only be a documented password waiting to be reused.
func newKernelSecret() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("generate kernel secret: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// defaultMihomoAPI is where mihomo's control API listens unless MIHOMO_API
// says otherwise. Configurable because 9090 is a popular port (Prometheus, for
// one) and a collision otherwise leaves the panel unable to start with no way out.
const defaultMihomoAPI = "127.0.0.1:9090"

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

// renamedEnv are names the panel used to accept. Refusing to start beats reading
// them silently: a stale UI_PASSWORD would otherwise be ignored and the panel
// would come up on the documented default password, which is far worse than a
// container that fails loudly once during an upgrade.
var renamedEnv = map[string]string{
	"UI_ADDR":       "renamed to UI_LISTEN",
	"MIHOMO_LISTEN": "renamed to MIHOMO_API",
	"PROXY_LISTEN":  "removed; the proxy port (mixed-port) now comes from override.yaml or the subscription",
	"MIHOMO_PROXY":  "removed; set HTTP_PROXY/HTTPS_PROXY to proxy subscription downloads",
	"STATIC_DIR":    "removed; the frontend is compiled into the binary",
}

func rejectRenamedEnv() {
	for old, why := range renamedEnv {
		if os.Getenv(old) != "" {
			log.Fatalf("%s is %s", old, why)
		}
	}
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
	rejectRenamedEnv()

	// DATA_HOME/
	//   mihomo/          kernel home (mihomo -d)
	//   ui/
	//     override.yaml  operator baseline (seeded from embed)
	//     settings.yaml  panel switches + configs list
	//     config/        config raw YAML
	// Relative defaults so a plain `./mihomo-ui` works in a checkout; the image
	// overrides both with absolute paths in its ENV.
	dataHome := env("DATA_HOME", "data")
	mihomoDir := filepath.Join(dataHome, "mihomo")
	uiDir := filepath.Join(dataHome, "ui")
	addr := env("UI_LISTEN", "0.0.0.0:7080")
	// MIHOMO_SECRET pins the kernel credential for external API clients; unset
	// (the default) mints a fresh random one every boot.
	secret := env("MIHOMO_SECRET", newKernelSecret())
	uiPassword := env("UI_PASSWORD", defaultUIPassword)
	// "./mihomo", not "mihomo": a bare name would be resolved through PATH and
	// could pick up an unrelated binary instead of the one sitting right here.
	mihomoBin := env("MIHOMO_BIN", "./mihomo")
	mihomoAPI := env("MIHOMO_API", defaultMihomoAPI)
	if _, _, err := configgen.SplitListen(mihomoAPI); err != nil {
		log.Fatalf("MIHOMO_API: %v", err)
	}
	configPath := filepath.Join(mihomoDir, "config.yaml")
	overridePath := filepath.Join(uiDir, "override.yaml")
	configDir := filepath.Join(uiDir, "config")

	for _, d := range []string{mihomoDir, uiDir, configDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Fatal(err)
		}
	}

	// Seed ui/override.yaml once from embedded template (never overwrite user edits).
	if err := configgen.EnsureOverride(overridePath); err != nil {
		log.Fatal(err)
	}

	def := configgen.DefaultUIStateFromOverride(overridePath)
	cfgStore, err := store.New(filepath.Join(uiDir, "settings.yaml"), store.UIPrefs{
		Mode:      def.Mode,
		LogLevel:  def.LogLevel,
		TunEnable: def.TunEnable,
	})
	if err != nil {
		log.Fatal(err)
	}

	// config ⊕ override ⊕ settings ⊕ forced -> config.yaml; forces secret/controller/profile.
	installOpts := configgen.InstallOptions{
		OverridePath: overridePath,
		ConfigDir:    configDir,
		Secret:       secret,
		KernelAPI:    mihomoAPI,
		UI:           configgen.UIStateFromPrefs(cfgStore.Prefs()),
	}
	if err := installBootConfig(configPath, cfgStore.ActiveList(), installOpts); err != nil {
		log.Fatal(err)
	}

	switch {
	case uiPassword == "":
		log.Printf("WARNING: UI_PASSWORD is empty - the panel API is completely open")
	case uiPassword == defaultUIPassword && !isLoopback(addr):
		log.Printf("WARNING: UI_PASSWORD is still the documented default and the panel")
		log.Printf("WARNING: listens on %s. Anyone who can reach that address can take", addr)
		log.Printf("WARNING: over the proxy. Set UI_PASSWORD to something private.")
	default:
		log.Printf("UI password auth enabled")
	}

	client := mihomo.NewClient("http://"+mihomoAPI, secret)

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
		UIPassword: uiPassword,
		Store:      cfgStore,
		ConfigPath: configPath,
		ConfigDir:  configDir,
		Config: &configsvc.Service{
			ConfigPath:   configPath,
			OverridePath: overridePath,
			ConfigDir:    configDir,
			Secret:       secret,
			KernelAPI:    mihomoAPI,
			Store:        cfgStore,
			Kernel:       client,
		},
	}

	httpSrv := &http.Server{Addr: addr, Handler: srv.Routes()}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("mihomo-ui listening on %s (data=%s bin=%s)", addr, dataHome, mihomoBin)
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
