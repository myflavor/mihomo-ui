package mihomo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	base   string
	secret string
	http   *http.Client
}

func NewClient(base, secret string) *Client {
	return &Client{
		base:   base,
		secret: secret,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("mihomo %s %s: %s (%s)", method, path, resp.Status, string(data))
	}
	if out == nil || len(data) == 0 || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (c *Client) Version(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/version", nil, &out)
}

func (c *Client) Configs(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/configs", nil, &out)
}

func (c *Client) PatchConfigs(ctx context.Context, patch map[string]any) error {
	return c.do(ctx, http.MethodPatch, "/configs", patch, nil)
}

func (c *Client) ReloadConfig(ctx context.Context, path string) error {
	return c.do(ctx, http.MethodPut, "/configs?force=true", map[string]any{"path": path}, nil)
}

func (c *Client) Proxies(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/proxies", nil, &out)
}

func (c *Client) SelectProxy(ctx context.Context, group, name string) error {
	escaped := url.PathEscape(group)
	return c.do(ctx, http.MethodPut, "/proxies/"+escaped, map[string]string{"name": name}, nil)
}

func (c *Client) ProxyDelay(ctx context.Context, name, testURL string, timeout int) (map[string]any, error) {
	if testURL == "" {
		testURL = "https://www.gstatic.com/generate_204"
	}
	if timeout <= 0 {
		timeout = 5000
	}
	escaped := url.PathEscape(name)
	q := url.Values{}
	q.Set("url", testURL)
	q.Set("timeout", fmt.Sprintf("%d", timeout))
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/proxies/"+escaped+"/delay?"+q.Encode(), nil, &out)
}

func (c *Client) UpdateProvider(ctx context.Context, name string) error {
	escaped := url.PathEscape(name)
	return c.do(ctx, http.MethodPut, "/providers/proxies/"+escaped, nil, nil)
}

func (c *Client) Providers(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/providers/proxies", nil, &out)
}

func (c *Client) GroupDelay(ctx context.Context, group, testURL string, timeout int) (map[string]any, error) {
	if testURL == "" {
		testURL = "https://www.gstatic.com/generate_204"
	}
	if timeout <= 0 {
		timeout = 5000
	}
	escaped := url.PathEscape(group)
	q := url.Values{}
	q.Set("url", testURL)
	q.Set("timeout", fmt.Sprintf("%d", timeout))
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/group/"+escaped+"/delay?"+q.Encode(), nil, &out)
}

func (c *Client) Connections(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/connections", nil, &out)
}

func (c *Client) CloseAllConnections(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/connections", nil, nil)
}

func (c *Client) CloseConnection(ctx context.Context, id string) error {
	escaped := url.PathEscape(id)
	return c.do(ctx, http.MethodDelete, "/connections/"+escaped, nil, nil)
}

func (c *Client) Rules(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, http.MethodGet, "/rules", nil, &out)
}

