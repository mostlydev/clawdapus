package runs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Client talks to a run supervisor. It authenticates the server in two ways:
// the socket and its directory must be owned by the expected UID and not
// writable by others, and the peer credential of every connection must be
// that UID (path checks alone race with rename).
type Client struct {
	hc *http.Client
}

// Dial prepares a client for the supervisor socket in dir run by serverUID.
func Dial(dir string, serverUID uint32) (*Client, error) {
	if err := CheckSocketDir(dir, serverUID); err != nil {
		return nil, fmt.Errorf("supervisor socket directory: %w", err)
	}
	path := filepath.Join(dir, SocketName)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if info.Mode()&os.ModeSocket == 0 || !ok || st.Uid != serverUID {
		return nil, fmt.Errorf("%s is not a socket owned by uid %d", path, serverUID)
	}
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "unix", path)
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(conn.(*net.UnixConn))
		if err != nil || uid != serverUID {
			conn.Close()
			return nil, fmt.Errorf("supervisor peer is not uid %d", serverUID)
		}
		return conn, nil
	}
	return &Client{hc: &http.Client{
		Transport: &http.Transport{DialContext: dial},
		Timeout:   maxWait + 15*time.Second,
	}}, nil
}

// Do sends one request and decodes a JSON response into out (if non-nil). A
// non-2xx response returns *APIError.
func (c *Client) Do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://claw-runs"+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var wrapped struct {
			Error *APIError `json:"error"`
		}
		if json.Unmarshal(raw, &wrapped) == nil && wrapped.Error != nil {
			wrapped.Error.Status = resp.StatusCode
			return wrapped.Error
		}
		return &APIError{Status: resp.StatusCode, Code: "http_error", Message: http.StatusText(resp.StatusCode)}
	}
	if out == nil {
		return nil
	}
	if b, ok := out.(*[]byte); ok {
		*b = raw
		return nil
	}
	return json.Unmarshal(raw, out)
}

// RunPath returns the API path for a run.
func RunPath(k Key, suffix string) string {
	return "/v1/runs/" + url.PathEscape(k.Installation) + "/" + url.PathEscape(k.RunID) + suffix
}
