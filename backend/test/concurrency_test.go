package test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// Raw config files are written without the service lock, so two writers for one
// id must still never publish a blend of both payloads. Readers run alongside
// and must never observe a partial file.
func TestConcurrentRawWritesForOneConfig(t *testing.T) {
	e := newEnv(t)
	cfg := e.addConfig("busy", sampleConfig("G1"))
	path := filepath.Join(e.ConfigDir, cfg.ID+".yaml")

	// Distinct lengths, so an interleaved leftover shows up as a parse failure
	// or as a mismatch against every individual payload.
	payload := func(i int) []byte {
		var b strings.Builder
		fmt.Fprintf(&b, "marker: writer-%d\nproxies:\n", i)
		for j := 0; j < (i+1)*40; j++ {
			fmt.Fprintf(&b, "  - {name: n%d-%d, type: direct}\n", i, j)
		}
		return []byte(b.String())
	}

	var badReads counter
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Yield: a tight read loop starves the writers it is racing.
				time.Sleep(time.Millisecond)
				raw, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				var doc map[string]any
				if err := yaml.Unmarshal(raw, &doc); err != nil || doc == nil {
					badReads.inc()
				}
			}
		}()
	}

	var writers sync.WaitGroup
	for w := 0; w < 8; w++ {
		writers.Add(1)
		go func(i int) {
			defer writers.Done()
			for n := 0; n < 25; n++ {
				if err := e.Svc.SaveRaw(cfg, payload(i)); err != nil {
					t.Errorf("writer %d: %v", i, err)
					return
				}
			}
		}(w)
	}
	writers.Wait()
	close(stop)
	readers.Wait()

	if n := badReads.get(); n > 0 {
		t.Fatalf("%d reads saw a corrupt or partial file", n)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for i := 0; i < 8; i++ {
		if string(raw) == string(payload(i)) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("published file matches no single writer (len=%d) — contents were blended", len(raw))
	}

	// Staging files must not accumulate.
	entries, err := os.ReadDir(e.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		if strings.Contains(ent.Name(), ".tmp") {
			t.Fatalf("leftover staging file: %s", ent.Name())
		}
	}
}

// config.yaml has exactly one writer, so hammering every path that touches it
// must always leave a parseable file with the pinned control-API keys intact.
func TestConfigYAMLStaysCoherentUnderMixedLoad(t *testing.T) {
	e := newEnv(t)
	a := e.addConfig("a", sampleConfig("ALPHA"))
	b := e.addConfig("b", sampleConfig("BETA"))
	if _, _, err := e.Svc.Activate(a.ID, false); err != nil {
		t.Fatal(err)
	}

	var badReads counter
	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			time.Sleep(time.Millisecond)
			raw, err := os.ReadFile(e.ConfigPath)
			if err != nil {
				continue
			}
			var doc map[string]any
			if err := yaml.Unmarshal(raw, &doc); err != nil || doc == nil {
				badReads.inc()
				continue
			}
			// The control API must never be knocked off loopback by a racing write.
			if got, ok := doc["external-controller"]; ok && got != "127.0.0.1:9090" {
				badReads.inc()
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 4 {
			case 0:
				_, _, _ = e.Svc.Activate(a.ID, false)
			case 1:
				_, _, _ = e.Svc.Activate(b.ID, false)
			case 2:
				_ = e.Svc.PatchRuntime(map[string]any{"mode": "global"})
			case 3:
				_, _ = e.Svc.ApplyActive()
			}
		}(i)
	}
	wg.Wait()
	close(stop)
	readers.Wait()

	if n := badReads.get(); n > 0 {
		t.Fatalf("%d reads saw a corrupt config.yaml or an unpinned controller", n)
	}
	doc := e.readConfigYAML()
	if doc["external-controller"] != "127.0.0.1:9090" {
		t.Fatalf("controller not pinned: %v", doc["external-controller"])
	}
	if doc["secret"] != "test-secret" {
		t.Fatalf("secret not forced: %v", doc["secret"])
	}
}

// Sessions are handed out and checked from many request goroutines at once.
func TestConcurrentLoginsAndChecks(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	var failures counter

	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok := e.login()
			res := e.do("GET", "/api/overview", tok, "")
			res.Body.Close()
			if res.StatusCode != 200 {
				failures.inc()
			}
			res = e.do("POST", "/api/logout", tok, "")
			res.Body.Close()
			res = e.do("GET", "/api/overview", tok, "")
			res.Body.Close()
			if res.StatusCode != 401 {
				failures.inc()
			}
		}()
	}
	wg.Wait()
	if n := failures.get(); n > 0 {
		t.Fatalf("%d session operations behaved incorrectly under load", n)
	}
}
