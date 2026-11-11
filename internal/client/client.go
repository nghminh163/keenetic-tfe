// Package client is a minimal Keenetic RCI (Remote Control Interface) client.
//
// Keenetic has no documented public API and no Terraform provider, so this talks to the same
// HTTP RCI the web UI uses. Auth is Keenetic's NDM challenge-response:
//
//	GET  /auth            -> 401 with headers X-NDM-Realm + X-NDM-Challenge (and a session cookie)
//	md5  = MD5(user ":" realm ":" pass)
//	sha  = SHA256(challenge + md5)
//	POST /auth {login,password:sha}  -> 200, session cookie now authenticated
//
// Config lives under interface Bridge<N> ("segments"). Reads use GET /rci/show/rc/interface;
// writes POST a command array to /rci/ and are then persisted with `system configuration save`.
package client

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"time"
)

// Client is a Keenetic RCI client. Safe for concurrent use.
type Client struct {
	endpoint string
	username string
	password string
	http     *http.Client

	// mu serializes a writer's whole read-modify-write-save sequence (the router applies config
	// non-atomically). Held by the Apply/Delete methods in segment.go/known_host.go/etc., which then
	// call get/post/save — so get/post must NOT take mu, or a writer self-deadlocks (mu is not
	// reentrant).
	mu sync.Mutex

	// authMu makes the NDM handshake atomic. The challenge is bound to the session cookie in the
	// shared jar, so two concurrent `GET /auth`+`POST /auth` pairs would overwrite each other's
	// cookie and one POST /auth would go out with the wrong session -> intermittent 401. Reads run
	// without mu (parallel, -parallelism=10), so the handshake needs its own lock. It is finer than
	// mu: only the brief handshake is serialized, the RCI reads/writes still run concurrently.
	authMu sync.Mutex
	authed bool // guarded by authMu; true once a session is established, so auth() skips the
	// GET /auth + POST /auth round-trips. A real 401 on an RCI call clears it (invalidateAuth) and
	// the get/post retry re-authenticates, so a server-side session expiry self-heals.
}

// New builds a client. endpoint is the router web base URL, e.g. "http://192.168.18.1".
func New(endpoint, username, password string) (*Client, error) {
	if endpoint == "" || username == "" || password == "" {
		return nil, fmt.Errorf("endpoint, username and password are all required")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Client{
		endpoint: strings.TrimRight(endpoint, "/"),
		username: username,
		password: password,
		http:     &http.Client{Jar: jar, Timeout: 30 * time.Second},
	}, nil
}

// auth performs the NDM handshake if the session is not already authenticated.
func (c *Client) auth(ctx context.Context) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if c.authed {
		return nil // session already established; a real 401 later clears this and re-auths
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/auth", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET /auth: %w", err)
	}
	defer drain(resp)

	switch resp.StatusCode {
	case http.StatusOK:
		c.authed = true
		return nil // session already valid
	case http.StatusUnauthorized:
		// fall through to challenge-response
	default:
		return fmt.Errorf("GET /auth: unexpected status %d", resp.StatusCode)
	}

	realm := resp.Header.Get("X-NDM-Realm")
	challenge := resp.Header.Get("X-NDM-Challenge")
	if realm == "" || challenge == "" {
		return fmt.Errorf("GET /auth: missing X-NDM-Realm/X-NDM-Challenge headers")
	}

	md5sum := md5.Sum([]byte(c.username + ":" + realm + ":" + c.password))
	shasum := sha256.Sum256([]byte(challenge + hex.EncodeToString(md5sum[:])))
	body, _ := json.Marshal(map[string]string{
		"login":    c.username,
		"password": hex.EncodeToString(shasum[:]),
	})

	preq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/auth", bytes.NewReader(body))
	if err != nil {
		return err
	}
	preq.Header.Set("Content-Type", "application/json")
	presp, err := c.http.Do(preq)
	if err != nil {
		return fmt.Errorf("POST /auth: %w", err)
	}
	defer drain(presp)
	if presp.StatusCode != http.StatusOK {
		return fmt.Errorf("authentication failed (status %d): check username/password", presp.StatusCode)
	}
	c.authed = true
	return nil
}

// invalidateAuth clears the cached session so the next auth() redoes the handshake. Called when an
// RCI call gets a 401 (the server-side session expired or was dropped).
func (c *Client) invalidateAuth() {
	c.authMu.Lock()
	c.authed = false
	c.authMu.Unlock()
}

// get fetches an RCI path and decodes the JSON body into out. Re-authenticates once on 401.
func (c *Client) get(ctx context.Context, path string, out interface{}) error {
	if err := c.auth(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		drain(resp)
		c.invalidateAuth()
		if err := c.auth(ctx); err != nil {
			return err
		}
		resp, err = c.http.Do(req.Clone(ctx))
		if err != nil {
			return err
		}
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// post sends a command array to /rci/ and returns the raw response for status inspection.
func (c *Client) post(ctx context.Context, cmd interface{}) ([]byte, error) {
	if err := c.auth(ctx); err != nil {
		return nil, err
	}
	body, err := json.Marshal(cmd)
	if err != nil {
		return nil, err
	}
	newReq := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/rci/", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}
	req, err := newReq()
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		drain(resp)
		c.invalidateAuth()
		if err := c.auth(ctx); err != nil {
			return nil, err
		}
		if req, err = newReq(); err != nil {
			return nil, err
		}
		if resp, err = c.http.Do(req); err != nil {
			return nil, err
		}
	}
	defer drain(resp)
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return raw, fmt.Errorf("POST /rci/: status %d: %s", resp.StatusCode, truncate(raw, 300))
	}
	if err := rciErrors(raw); err != nil {
		return raw, err
	}
	return raw, nil
}

// save persists the running configuration (otherwise changes are lost on reboot).
func (c *Client) save(ctx context.Context) error {
	cmd := []map[string]interface{}{
		{"system": map[string]interface{}{"configuration": map[string]interface{}{"save": map[string]interface{}{}}}},
	}
	_, err := c.post(ctx, cmd)
	return err
}

func drain(resp *http.Response) {
	if resp == nil {
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// rciErrors walks the RCI response tree and returns a combined error for any {"status":"error"}.
func rciErrors(raw []byte) error {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil // not JSON we understand; let callers treat 200 as success
	}
	var msgs []string
	var walk func(x interface{})
	walk = func(x interface{}) {
		switch t := x.(type) {
		case map[string]interface{}:
			if s, ok := t["status"].(string); ok && s == "error" {
				if m, ok := t["message"].(string); ok {
					msgs = append(msgs, m)
				} else {
					msgs = append(msgs, "unspecified RCI error")
				}
			}
			for _, vv := range t {
				walk(vv)
			}
		case []interface{}:
			for _, vv := range t {
				walk(vv)
			}
		}
	}
	walk(v)
	if len(msgs) > 0 {
		return fmt.Errorf("RCI error: %s", strings.Join(msgs, "; "))
	}
	return nil
}
