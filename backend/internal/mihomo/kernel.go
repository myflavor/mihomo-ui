package mihomo

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
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
