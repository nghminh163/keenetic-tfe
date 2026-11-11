package client

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeRouter mimics Keenetic's NDM auth: every unauthenticated GET /auth starts a NEW session (new
// cookie + new challenge), and POST /auth only succeeds when the answer matches the challenge of the
// session whose cookie it carries.
type fakeRouter struct {
	mu        sync.Mutex
	next      int
	challenge map[string]string // session -> challenge
	authed    map[string]bool
}

func newFakeRouter() *fakeRouter {
	return &fakeRouter{challenge: map[string]string{}, authed: map[string]bool{}}
}

func (f *fakeRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess := ""
	if ck, err := r.Cookie("sid"); err == nil {
		sess = ck.Value
	}
	switch {
	case r.URL.Path == "/auth" && r.Method == http.MethodGet:
		if f.authed[sess] {
			w.WriteHeader(http.StatusOK)
			return
		}
		f.next++
		sess = fmt.Sprintf("s%d", f.next)
		f.challenge[sess] = fmt.Sprintf("c%d", f.next)
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: sess, Path: "/"})
		w.Header().Set("X-NDM-Realm", "realm")
		w.Header().Set("X-NDM-Challenge", f.challenge[sess])
		w.WriteHeader(http.StatusUnauthorized)
	case r.URL.Path == "/auth" && r.Method == http.MethodPost:
		var body struct{ Login, Password string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		m := md5.Sum([]byte("admin:realm:secret"))
		want := sha256.Sum256([]byte(f.challenge[sess] + hex.EncodeToString(m[:])))
		if f.challenge[sess] == "" || body.Password != hex.EncodeToString(want[:]) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.authed[sess] = true
		w.WriteHeader(http.StatusOK)
	default:
		if !f.authed[sess] {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}
}

// Tofu refreshes resources in parallel: many concurrent reads must not break each other's handshake.
func TestConcurrentReadsAuthenticate(t *testing.T) {
	srv := httptest.NewServer(newFakeRouter())
	defer srv.Close()
	c, err := New(srv.URL, "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out map[string]interface{}
			errs <- c.get(context.Background(), "/rci/show/rc/interface", &out)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent get: %v", err)
		}
	}
}
