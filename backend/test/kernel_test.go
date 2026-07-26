package test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xin/mihomo-ui/internal/mihomo"
)

// fakeBin writes a shell script that behaves like a kernel for the bits Kernel
// cares about: it runs until signalled, or exits with a code on demand.
func fakeBin(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-mihomo")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// Done must be a broadcast. It previously handed the single exit value to
// whichever goroutine received first, and Stop hung forever when it lost.
func TestDoneReachesEveryWaiter(t *testing.T) {
	k := &mihomo.Kernel{Bin: fakeBin(t, "sleep 30"), Home: t.TempDir()}
	if err := k.Start(); err != nil {
		t.Fatal(err)
	}

	const waiters = 8
	got := make(chan struct{}, waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			<-k.Done()
			got <- struct{}{}
		}()
	}

	k.Stop()
	for i := 0; i < waiters; i++ {
		select {
		case <-got:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d waiters were released", i, waiters)
		}
	}
}

// Stop must return promptly even with another goroutine already parked on
// Done — the exact shape that used to deadlock about half the time.
func TestStopReturnsWhileAnotherGoroutineWaits(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		k := &mihomo.Kernel{Bin: fakeBin(t, "sleep 30"), Home: t.TempDir()}
		if err := k.Start(); err != nil {
			t.Fatal(err)
		}
		watching := make(chan struct{})
		go func() { <-k.Done(); close(watching) }()

		done := make(chan struct{})
		go func() { k.Stop(); close(done) }()
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			t.Fatalf("attempt %d: Stop hung with a concurrent Done waiter", attempt)
		}
		select {
		case <-watching:
		case <-time.After(2 * time.Second):
			t.Fatalf("attempt %d: the watchdog never saw the exit", attempt)
		}
	}
}

func TestStopIsIdempotent(t *testing.T) {
	k := &mihomo.Kernel{Bin: fakeBin(t, "sleep 30"), Home: t.TempDir()}
	if err := k.Start(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		done := make(chan struct{})
		go func() { k.Stop(); close(done) }()
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			t.Fatalf("Stop call %d hung", i+1)
		}
	}
}

// A kernel that dies on its own must report why, so the supervisor can log it.
func TestErrCarriesTheExitReason(t *testing.T) {
	k := &mihomo.Kernel{Bin: fakeBin(t, "exit 3"), Home: t.TempDir()}
	if err := k.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-k.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done never fired for a kernel that exited")
	}
	if k.Err() == nil {
		t.Fatal("Err is nil after a non-zero exit")
	}
}

func TestCleanExitReportsNoError(t *testing.T) {
	k := &mihomo.Kernel{Bin: fakeBin(t, "exit 0"), Home: t.TempDir()}
	if err := k.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-k.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done never fired")
	}
	if k.Err() != nil {
		t.Fatalf("Err = %v for a clean exit", k.Err())
	}
}

// If Start fails, Done must still be closed: a caller waiting on a kernel that
// never ran would otherwise block forever.
func TestFailedStartStillClosesDone(t *testing.T) {
	cases := map[string]*mihomo.Kernel{
		"missing home":   {Bin: fakeBin(t, "sleep 1")},
		"missing binary": {Bin: filepath.Join(t.TempDir(), "nope"), Home: t.TempDir()},
	}
	for name, k := range cases {
		t.Run(name, func(t *testing.T) {
			if err := k.Start(); err == nil {
				t.Fatal("Start unexpectedly succeeded")
			}
			select {
			case <-k.Done():
			case <-time.After(2 * time.Second):
				t.Fatal("Done blocks after a failed Start")
			}
		})
	}
}

// Stop on a kernel that never started must be a no-op, not a panic or a hang.
func TestStopWithoutStartIsSafe(t *testing.T) {
	k := &mihomo.Kernel{}
	done := make(chan struct{})
	go func() { k.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop hung on a kernel that never started")
	}
}

func TestWaitReadyFailsFastWhenKernelExits(t *testing.T) {
	k := &mihomo.Kernel{Bin: fakeBin(t, "exit 1"), Home: t.TempDir()}
	if err := k.Start(); err != nil {
		t.Fatal(err)
	}
	client := mihomo.NewClient("http://127.0.0.1:1", "") // nothing listens here
	start := time.Now()
	err := k.WaitReady(client, 10*time.Second)
	if err == nil {
		t.Fatal("WaitReady reported success for a dead kernel")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("WaitReady took %s; it should notice the exit rather than wait out the timeout", elapsed)
	}
}

// A 401 from the control address means something else already owns it. Waiting
// out the timeout would only delay the same failure with a vaguer message.
func TestWaitReadyFailsFastOnForeignKernel(t *testing.T) {
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
	}))
	defer foreign.Close()

	k := &mihomo.Kernel{Bin: fakeBin(t, "sleep 30"), Home: t.TempDir()}
	if err := k.Start(); err != nil {
		t.Fatal(err)
	}
	defer k.Stop()

	start := time.Now()
	err := k.WaitReady(mihomo.NewClient(foreign.URL, "our-secret"), 10*time.Second)
	if !errors.Is(err, mihomo.ErrForeignKernel) {
		t.Fatalf("err = %v, want ErrForeignKernel", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("took %s; a 401 should fail immediately, not wait out the timeout", elapsed)
	}
	if !strings.Contains(err.Error(), "MIHOMO_API") {
		t.Fatalf("error should name the way out, got: %v", err)
	}
}
