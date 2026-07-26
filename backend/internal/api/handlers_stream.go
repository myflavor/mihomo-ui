package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// defaultMihomoURL is where the kernel listens when MIHOMO_API is unset.
const defaultMihomoURL = "http://127.0.0.1:9090"

// logPingInterval keeps an idle log stream from being reaped by proxies.
const logPingInterval = 15 * time.Second

// streamClient has no timeout: these responses are open-ended by design.
var streamClient = &http.Client{}

// ndjsonStream is the browser side of a proxied stream. Writes are serialized so
// handleLogs' heartbeat cannot interleave a line with the copy loop.
type ndjsonStream struct {
	mu      sync.Mutex
	w       http.ResponseWriter
	flusher http.Flusher
}

// startNDJSON commits headers first: mihomo may withhold its own until an event.
func startNDJSON(w http.ResponseWriter) (*ndjsonStream, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]string{"error": "streaming unsupported"})
		return nil, false
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return &ndjsonStream{w: w, flusher: flusher}, true
}

func (n *ndjsonStream) write(b []byte) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, err := n.w.Write(b); err != nil {
		return err
	}
	n.flusher.Flush()
	return nil
}

// event writes one line of our own, unlike kernel bytes which pass verbatim.
func (n *ndjsonStream) event(typ, payload string) error {
	line, err := json.Marshal(map[string]string{"type": typ, "payload": payload})
	if err != nil {
		return err
	}
	return n.write(append(line, '\n'))
}

// copyFrom forwards upstream bytes until either end stops.
func (n *ndjsonStream) copyFrom(src io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		read, err := src.Read(buf)
		if read > 0 {
			if werr := n.write(buf[:read]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// openMihomoStream attaches to a kernel endpoint; on success the caller owns Body.
func (s *Server) openMihomoStream(ctx context.Context, path string) (*http.Response, error) {
	base := strings.TrimRight(s.MihomoURL, "/")
	if base == "" {
		base = defaultMihomoURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, err
	}
	if s.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+s.Secret)
	}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("upstream %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

// handleTraffic proxies mihomo's realtime traffic NDJSON stream.
func (s *Server) handleTraffic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errMethod)
		return
	}
	s.proxyMihomoStream(w, r, "/traffic")
}

// proxyMihomoStream pipes a kernel stream verbatim; failures are silent because
// headers are already sent and there is no status code left to set.
func (s *Server) proxyMihomoStream(w http.ResponseWriter, r *http.Request, path string) {
	stream, ok := startNDJSON(w)
	if !ok {
		return
	}
	resp, err := s.openMihomoStream(r.Context(), path)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	stream.copyFrom(resp.Body)
}

// handleLogs proxies mihomo's log stream as NDJSON. Unlike handleTraffic it
// reports failures in-band and heartbeats, so an idle view differs from a dead
// one. ?level= is the stream's own filter, separate from the kernel log-level.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, errMethod)
		return
	}
	stream, ok := startNDJSON(w)
	if !ok {
		return
	}

	level := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("level")))
	switch level {
	case "debug", "info", "warning", "error", "silent":
	default:
		// match mihomo getLogs default
		level = "info"
	}

	// Unblock the browser immediately; an idle mihomo /logs sends nothing.
	if err := stream.event("connected", "log stream ready"); err != nil {
		return
	}

	resp, err := s.openMihomoStream(r.Context(), "/logs?level="+level)
	if err != nil {
		_ = stream.event("error", err.Error())
		return
	}
	defer resp.Body.Close()

	// Both writers must finish before we return, or a goroutine parked on stream.mu
	// writes after ServeHTTP. Defers are LIFO: cancel() runs before wg.Wait().
	ctx, cancel := context.WithCancel(r.Context())
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(logPingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := stream.event("ping", ""); err != nil {
					return
				}
			}
		}
	}()

	stream.copyFrom(resp.Body)
}
