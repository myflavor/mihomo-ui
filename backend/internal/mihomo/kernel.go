package mihomo

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
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
	// exited closes when the child is reaped; waitErr holds why. Closing rather
	// than sending lets Stop and the watchdog both see it, instead of racing.
	exited  chan struct{}
	waitErr error
	once    sync.Once
}

// Start launches mihomo -d Home; on failure exited closes so Done never blocks.
func (k *Kernel) Start() error {
	k.exited = make(chan struct{})
	fail := func(err error) error {
		close(k.exited)
		return err
	}
	if k.Bin == "" {
		k.Bin = "mihomo"
	}
	if k.Home == "" {
		return fail(fmt.Errorf("mihomo home empty"))
	}
	if err := os.MkdirAll(k.Home, 0o755); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	k.cancel = cancel
	cmd := exec.CommandContext(ctx, k.Bin, "-d", k.Home)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		cancel()
		return fail(fmt.Errorf("start mihomo: %w", err))
	}
	k.cmd = cmd
	go func() {
		k.waitErr = cmd.Wait()
		close(k.exited)
	}()
	log.Printf("mihomo started pid=%d home=%s", cmd.Process.Pid, k.Home)
	return nil
}

// Done is closed when the mihomo process exits. Safe for multiple waiters.
func (k *Kernel) Done() <-chan struct{} {
	return k.exited
}

// Err returns why mihomo exited. Only valid after Done is closed.
func (k *Kernel) Err() error {
	return k.waitErr
}

// ErrForeignKernel means the control address answered but rejected our
// credential — something else is already listening there.
var ErrForeignKernel = errors.New("another mihomo is already listening on that address")

// WaitReady polls the external-controller until Version succeeds or timeout.
func (k *Kernel) WaitReady(client *Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		select {
		case <-k.exited:
			if k.waitErr != nil {
				return fmt.Errorf("mihomo exited early: %w", k.waitErr)
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
		// A 401 is not a "not up yet" condition: our own kernel would accept the
		// secret we just generated for it. Someone else owns this address, so
		// waiting out the timeout would only delay the same failure.
		var se *StatusError
		if errors.As(err, &se) && se.Code == http.StatusUnauthorized {
			return fmt.Errorf("%w: %s — set MIHOMO_API to a free address", ErrForeignKernel, client.base)
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
		case <-k.exited:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			select {
			case <-k.exited:
			case <-time.After(5 * time.Second):
				log.Printf("mihomo pid=%d still not reaped after SIGKILL", pid)
			}
		}
		if k.cancel != nil {
			k.cancel()
		}
		log.Printf("mihomo stopped")
	})
}
