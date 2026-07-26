package test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xin/mihomo-ui/internal/configsvc"
	"github.com/xin/mihomo-ui/internal/store"
)

// Activate must flip the store flag and install in one step. If it can be
// observed half-done, settings.yaml and config.yaml name different configs.
func TestActivateInstallsAndPersists(t *testing.T) {
	e := newEnv(t)
	cfg := e.addConfig("first", sampleConfig("G1"))

	got, res, err := e.Svc.Activate(cfg.ID, false)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !got.Active || got.ID != cfg.ID {
		t.Fatalf("returned config not active: %+v", got)
	}
	if res == nil || res.OK != 1 {
		t.Fatalf("apply result not ok: %+v", res)
	}
	if e.activeID() != cfg.ID {
		t.Fatalf("settings.yaml active = %q, want %q", e.activeID(), cfg.ID)
	}
	doc := e.readConfigYAML()
	groups, _ := doc["proxy-groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("config.yaml missing the config's groups: %v", doc["proxy-groups"])
	}
	if e.Kernel.reloadCount() != 1 {
		t.Fatalf("reloads = %d, want 1", e.Kernel.reloadCount())
	}
}

func TestActivateUnknownIDReportsNotFound(t *testing.T) {
	e := newEnv(t)
	_, _, err := e.Svc.Activate("no-such-id", false)
	if !errors.Is(err, configsvc.ErrNoSuchConfig) {
		t.Fatalf("err = %v, want ErrNoSuchConfig", err)
	}
	if e.Kernel.reloadCount() != 0 {
		t.Fatal("kernel was reloaded for an unknown config")
	}
}

// A config whose raw file will not parse must not become the active one:
// settings.yaml would then disagree with the config.yaml the kernel is running.
func TestActivateRejectsUnparseableRawBeforeFlippingActive(t *testing.T) {
	e := newEnv(t)
	good := e.addConfig("good", sampleConfig("G1"))
	if _, _, err := e.Svc.Activate(good.ID, false); err != nil {
		t.Fatal(err)
	}
	bad := e.addConfig("bad", sampleConfig("G2"))
	// Corrupt it behind the panel's back, the way a hand edit would.
	raw := filepath.Join(e.ConfigDir, bad.ID+".yaml")
	if err := os.WriteFile(raw, []byte("just a plain string\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := e.Svc.Activate(bad.ID, false); err == nil {
		t.Fatal("activate accepted an unparseable config")
	}
	if got := e.activeID(); got != good.ID {
		t.Fatalf("active flipped to the broken config: %q", got)
	}
	doc := e.readConfigYAML()
	if groups, _ := doc["proxy-groups"].([]any); len(groups) != 1 {
		t.Fatal("config.yaml was rewritten despite the failure")
	}
}

// Refresh decides from the store, not from the caller's pre-download snapshot.
// A config activated while the download ran must still get installed.
func TestRefreshInstallsWhenActivatedDuringDownload(t *testing.T) {
	e := newEnv(t)
	cfg := e.addConfig("sub", sampleConfig("G1"))

	// snapshot taken while inactive, exactly as a handler would pass it
	snapshot, err := e.Store.Get(cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Active {
		t.Fatal("precondition: config should start inactive")
	}
	if _, err := e.Store.SetActive(cfg.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := e.Svc.Refresh(snapshot, false); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if e.Kernel.reloadCount() == 0 {
		t.Fatal("refresh skipped the install using a stale inactive snapshot")
	}
}

// The mirror of the above: deactivated during the download means leave the
// kernel alone rather than reverting it to this config.
func TestRefreshSkipsInstallWhenDeactivatedDuringDownload(t *testing.T) {
	e := newEnv(t)
	a := e.addConfig("a", sampleConfig("GA"))
	b := e.addConfig("b", sampleConfig("GB"))
	if _, _, err := e.Svc.Activate(a.ID, false); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := e.Store.Get(a.ID) // active at this point
	if !snapshot.Active {
		t.Fatal("precondition: a should be active")
	}
	if _, _, err := e.Svc.Activate(b.ID, false); err != nil {
		t.Fatal(err)
	}
	before := e.Kernel.reloadCount()

	if _, err := e.Svc.Refresh(snapshot, false); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if e.Kernel.reloadCount() != before {
		t.Fatal("refresh reinstalled a config that is no longer active")
	}
	if e.activeID() != b.ID {
		t.Fatalf("active = %q, want %q", e.activeID(), b.ID)
	}
}

// Deleting a config while its raw file is being written must not leave the
// subscription (and its credentials) orphaned on disk.
func TestRawFileDroppedWhenConfigDeletedConcurrently(t *testing.T) {
	e := newEnv(t)
	cfg := e.addConfig("doomed", sampleConfig("G1"))
	if err := e.Store.Delete(cfg.ID); err != nil {
		t.Fatal(err)
	}
	// A write that was already in flight lands after the delete.
	if err := e.Svc.SaveRaw(cfg, []byte(sampleConfig("G1"))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.ConfigDir, cfg.ID+".yaml")); !os.IsNotExist(err) {
		t.Fatal("raw file for a deleted config survived on disk")
	}
}

// The whole point of keeping downloads out of the lock: a slow kernel reload
// must not stop other config work from making progress... but it must still
// serialize, so config.yaml is never written by two goroutines at once.
func TestSlowReloadDoesNotDeadlockOtherCalls(t *testing.T) {
	e := newEnv(t)
	cfg := e.addConfig("c", sampleConfig("G1"))
	release := e.Kernel.blockReloads()
	// Must run even if an assertion below fails: a still-blocked handler makes
	// httptest.Server.Close hang, turning any failure into a test timeout.
	defer release()

	done := make(chan error, 1)
	go func() {
		_, _, err := e.Svc.Activate(cfg.ID, false)
		done <- err
	}()
	waitFor(t, "reload to be in flight", func() bool { return e.Kernel.reloadCount() > 0 })

	// A second caller queues behind the lock rather than corrupting the file.
	second := make(chan error, 1)
	go func() { second <- e.Svc.PatchRuntime(map[string]any{"mode": "global"}) }()
	select {
	case <-second:
		t.Fatal("PatchRuntime ran while another install held the lock")
	case <-time.After(150 * time.Millisecond):
	}

	release()
	if err := <-done; err != nil {
		t.Fatalf("activate: %v", err)
	}
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("patch after release: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PatchRuntime never completed after the lock was released")
	}
}

// Concurrent activations must leave the store and the file agreeing on one
// config — whichever wins — never a mix.
func TestConcurrentActivationsConvergeOnOneConfig(t *testing.T) {
	e := newEnv(t)
	var cfgs []store.Config
	for i, name := range []string{"a", "b", "c", "d"} {
		cfgs = append(cfgs, e.addConfig(name, sampleConfig(strings.ToUpper(name))))
		_ = i
	}

	var wg sync.WaitGroup
	for round := 0; round < 8; round++ {
		for _, c := range cfgs {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				_, _, _ = e.Svc.Activate(id, false)
			}(c.ID)
		}
	}
	wg.Wait()

	active := e.activeID()
	if active == "" {
		t.Fatal("no config is active after concurrent activations")
	}
	// config.yaml must hold exactly the winner's group, not a blend.
	doc := e.readConfigYAML()
	groups, _ := doc["proxy-groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("config.yaml has %d groups, want exactly 1", len(groups))
	}
	g, _ := groups[0].(map[string]any)
	name, _ := g["name"].(string)
	var wantName string
	for _, c := range cfgs {
		if c.ID == active {
			wantName = strings.ToUpper(c.Name)
		}
	}
	if name != wantName {
		t.Fatalf("config.yaml runs group %q but settings.yaml says %q is active", name, wantName)
	}
}

// PatchRuntime is what makes a mode/TUN toggle survive a later config switch.
func TestPatchRuntimePersistsIntoConfigYAML(t *testing.T) {
	e := newEnv(t)
	cfg := e.addConfig("c", sampleConfig("G1"))
	if _, _, err := e.Svc.Activate(cfg.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := e.Svc.PatchRuntime(map[string]any{"mode": "global"}); err != nil {
		t.Fatal(err)
	}
	if got := e.readConfigYAML()["mode"]; got != "global" {
		t.Fatalf("mode = %v, want global", got)
	}
}

// WriteRaw reports the write and the reload separately: a rejected reload still
// leaves valid content on disk, and the caller says so differently.
func TestWriteRawSeparatesWriteAndReloadErrors(t *testing.T) {
	e := newEnv(t)
	content := []byte("mode: direct\n")

	writeErr, reloadErr := e.Svc.WriteRaw(content, true)
	if writeErr != nil || reloadErr != nil {
		t.Fatalf("clean write: write=%v reload=%v", writeErr, reloadErr)
	}
	if got := e.readConfigYAML()["mode"]; got != "direct" {
		t.Fatalf("mode = %v, want direct", got)
	}

	e.Kernel.mu.Lock()
	e.Kernel.failReloa = true
	e.Kernel.mu.Unlock()

	writeErr, reloadErr = e.Svc.WriteRaw([]byte("mode: rule\n"), true)
	if writeErr != nil {
		t.Fatalf("write should still succeed: %v", writeErr)
	}
	if reloadErr == nil {
		t.Fatal("reload failure was not reported")
	}
	if got := e.readConfigYAML()["mode"]; got != "rule" {
		t.Fatalf("content should be on disk despite the reload failure, got %v", got)
	}
}

// ApplyActive with nothing active must still leave a bootable config.yaml.
func TestApplyActiveWithNoConfigWritesBootableShell(t *testing.T) {
	e := newEnv(t)
	if _, err := e.Svc.ApplyActive(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	doc := e.readConfigYAML()
	if _, ok := doc["rules"]; !ok {
		t.Fatal("shell config has no rules")
	}
	if doc["external-controller"] != "127.0.0.1:9090" {
		t.Fatalf("control API not pinned to loopback: %v", doc["external-controller"])
	}
	if doc["secret"] != "test-secret" {
		t.Fatalf("secret not forced from env: %v", doc["secret"])
	}
}

// forceRefresh must actually re-download. Without a test here, ignoring the
// parameter entirely goes unnoticed — mutation testing found exactly that gap.
func TestActivateWithForceRefreshRedownloads(t *testing.T) {
	e := newEnv(t)
	var hits counter
	body := sampleConfig("FRESH")
	sub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.inc()
		_, _ = io.WriteString(w, body)
	}))
	defer sub.Close()

	cfg, err := e.Store.Add("remote", sub.URL, "url", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Seed a stale local copy so a non-refreshing activate would use it.
	if err := e.Svc.SaveRaw(cfg, []byte(sampleConfig("STALE"))); err != nil {
		t.Fatal(err)
	}

	if _, _, err := e.Svc.Activate(cfg.ID, false); err != nil {
		t.Fatal(err)
	}
	if hits.get() != 0 {
		t.Fatalf("activate without forceRefresh downloaded %d times", hits.get())
	}
	if !hasGroup(e.readConfigYAML(), "STALE") {
		t.Fatal("expected the cached copy to be installed")
	}

	if _, _, err := e.Svc.Activate(cfg.ID, true); err != nil {
		t.Fatal(err)
	}
	if hits.get() != 1 {
		t.Fatalf("forceRefresh triggered %d downloads, want 1", hits.get())
	}
	if !hasGroup(e.readConfigYAML(), "FRESH") {
		t.Fatal("forceRefresh did not install the freshly downloaded config")
	}
}

// Refresh with forceRefresh is the "更新" button; it must re-fetch too.
func TestRefreshForceRedownloads(t *testing.T) {
	e := newEnv(t)
	var hits counter
	sub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.inc()
		_, _ = io.WriteString(w, sampleConfig("PULLED"))
	}))
	defer sub.Close()

	cfg, err := e.Store.Add("remote", sub.URL, "url", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Svc.SaveRaw(cfg, []byte(sampleConfig("OLD"))); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Svc.Activate(cfg.ID, false); err != nil {
		t.Fatal(err)
	}
	active, _ := e.Store.Get(cfg.ID)

	if _, err := e.Svc.Refresh(active, true); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if hits.get() != 1 {
		t.Fatalf("refresh downloaded %d times, want 1", hits.get())
	}
	if !hasGroup(e.readConfigYAML(), "PULLED") {
		t.Fatal("refresh did not install the downloaded config")
	}
}

// Editing a subscription downloads before anything is committed, so a bad URL
// leaves the entry exactly as it was rather than persisting a name and address
// whose content was never fetched.
func TestUpdateMetaDownloadsBeforeCommitting(t *testing.T) {
	e := newEnv(t)
	var hits counter
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.inc()
		_, _ = io.WriteString(w, sampleConfig("PULLED"))
	}))
	defer good.Close()
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer dead.Close()

	cfg, err := e.Store.Add("sub", good.URL, "url", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Svc.SaveRaw(cfg, []byte(sampleConfig("ORIGINAL"))); err != nil {
		t.Fatal(err)
	}

	// A rename alone still re-fetches: the remote may have changed either way.
	newName := "renamed"
	got, _, err := e.Svc.UpdateMeta(cfg.ID, store.ConfigPatch{Name: &newName})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got.Name != newName {
		t.Fatalf("name = %q, want %q", got.Name, newName)
	}
	if hits.get() != 1 {
		t.Fatalf("rename triggered %d downloads, want 1", hits.get())
	}

	// Pointing at a URL that fails must not persist the new name or URL.
	badName, badURL := "should-not-stick", dead.URL
	if _, _, err := e.Svc.UpdateMeta(cfg.ID, store.ConfigPatch{Name: &badName, URL: &badURL}); err == nil {
		t.Fatal("UpdateMeta accepted a URL that could not be fetched")
	}
	after, err := e.Store.Get(cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != newName {
		t.Fatalf("name changed despite the failed fetch: %q", after.Name)
	}
	if after.URL != good.URL {
		t.Fatalf("url changed despite the failed fetch: %q", after.URL)
	}
}

// A kernel that refuses the config must not leave the panel claiming it is live.
func TestActivateRollsBackWhenKernelRejectsReload(t *testing.T) {
	e := newEnv(t)
	a := e.addConfig("a", sampleConfig("ALPHA"))
	b := e.addConfig("b", sampleConfig("BETA"))
	if _, _, err := e.Svc.Activate(a.ID, false); err != nil {
		t.Fatal(err)
	}

	e.Kernel.mu.Lock()
	e.Kernel.failReloa = true
	e.Kernel.mu.Unlock()

	if _, _, err := e.Svc.Activate(b.ID, false); err == nil {
		t.Fatal("activate reported success even though the kernel refused the reload")
	}
	if got := e.activeID(); got != a.ID {
		t.Fatalf("settings.yaml active = %q, want the previous config %q", got, a.ID)
	}
	cur, err := e.Store.Get(a.ID)
	if err != nil || !cur.Active {
		t.Fatalf("in-memory store did not roll back: %+v", cur)
	}
}
