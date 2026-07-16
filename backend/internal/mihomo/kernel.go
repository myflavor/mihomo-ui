package mihomo

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Kernel manages a mihomo child process.
type Kernel struct {
	Bin  string // path to mihomo binary
	Home string // -d directory (contains config.yaml)

	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
}

// Start launches mihomo -d Home.
func (k *Kernel) Start() error {
	if k.Bin == "" {
		k.Bin = "mihomo"
	}
	if k.Home == "" {
		return fmt.Errorf("mihomo home empty")
	}
	if err := os.MkdirAll(k.Home, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	k.cancel = cancel
	k.done = make(chan error, 1)
	cmd := exec.CommandContext(ctx, k.Bin, "-d", k.Home)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start mihomo: %w", err)
	}
	k.cmd = cmd
	go func() {
		err := cmd.Wait()
		k.done <- err
	}()
	log.Printf("mihomo started pid=%d home=%s", cmd.Process.Pid, k.Home)
	return nil
}

// Done is closed/sent when the mihomo process exits.
func (k *Kernel) Done() <-chan error {
	return k.done
}

// WaitReady polls the external-controller until Version succeeds or timeout.
func (k *Kernel) WaitReady(client *Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		select {
		case err := <-k.done:
			if err != nil {
				return fmt.Errorf("mihomo exited early: %w", err)
			}
			return fmt.Errorf("mihomo exited early")
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
		_, err := client.Version(ctx)
		cancel()
		if err == nil {
			return nil
		}
		last = err
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("mihomo API not ready after %s: %v", timeout, last)
}

// Stop terminates mihomo and waits for it to exit (idempotent).
func (k *Kernel) Stop() {
	k.once.Do(func() {
		if k.cmd == nil || k.cmd.Process == nil {
			return
		}
		pid := k.cmd.Process.Pid
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		select {
		case <-k.done:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			<-k.done
		}
		if k.cancel != nil {
			k.cancel()
		}
		log.Printf("mihomo stopped")
	})
}

// EnsureMinimalConfig writes a bootable config.yaml if missing.
// Full merge is done later by the UI install pipeline.
func EnsureMinimalConfig(configPath, secret string) error {
	if _, err := os.Stat(configPath); err == nil {
		return patchBootKeys(configPath, secret)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	if secret == "" {
		secret = "mihomo"
	}
	body := fmt.Sprintf(`mixed-port: 7890
allow-lan: false
bind-address: 127.0.0.1
mode: rule
log-level: info
external-controller: 127.0.0.1:9090
secret: %q
proxies: []
proxy-groups: []
rules:
  - MATCH,DIRECT
`, secret)
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, configPath); err != nil {
		return err
	}
	log.Printf("seeded minimal %s", configPath)
	return nil
}

// patchBootKeys ensures secret / external-controller stay present on restart.
func patchBootKeys(configPath, secret string) error {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	text := string(raw)
	changed := false
	if secret != "" && !containsKey(text, "secret") {
		text += fmt.Sprintf("\nsecret: %q\n", secret)
		changed = true
	}
	if !containsKey(text, "external-controller") {
		text += "\nexternal-controller: 127.0.0.1:9090\n"
		changed = true
	}
	if !changed {
		return nil
	}
	return os.WriteFile(configPath, []byte(text), 0o644)
}

func containsKey(yamlText, key string) bool {
	for _, line := range splitLines(yamlText) {
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		i := 0
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		rest := line[i:]
		if len(rest) >= len(key)+1 && rest[:len(key)] == key && rest[len(key)] == ':' {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start <= len(s) {
		out = append(out, s[start:])
	}
	return out
}
