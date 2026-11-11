package client

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

// TestWriterPatternNoDeadlock reproduces what the Apply/Delete methods do — hold c.mu, then call
// get/post — which self-deadlocked when get/post also took c.mu. get is read-only here, so this is
// safe against a live router. Fails (via timeout) if the writer+get pattern ever deadlocks again.
func TestWriterPatternNoDeadlock(t *testing.T) {
	ep := os.Getenv("KEENETIC_ENDPOINT")
	user := os.Getenv("KEENETIC_USERNAME")
	pass := os.Getenv("KEENETIC_PASSWORD")
	if ep == "" || user == "" || pass == "" {
		t.Skip("set KEENETIC_ENDPOINT/USERNAME/PASSWORD to run the live concurrency test")
	}
	c, err := New(ep, user, pass)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		c.mu.Lock() // a writer holds mu for its whole sequence...
		defer c.mu.Unlock()
		var out map[string]interface{}
		done <- c.get(context.Background(), "/rci/show/rc/interface", &out) // ...then calls get
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("get under held mu failed: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("deadlock: a writer holding mu + calling get did not return within 15s")
	}
}

// TestConcurrentGet exercises the shared Client the way Terraform does: many resource reads in
// parallel on one *Client. Before the mu fix in client.go this 401'd intermittently, because the
// NDM challenge is bound to the session cookie in the shared jar and concurrent auth handshakes
// overwrote each other's cookie. It is skipped unless the router env vars are set (live router).
func TestConcurrentGet(t *testing.T) {
	ep := os.Getenv("KEENETIC_ENDPOINT")
	user := os.Getenv("KEENETIC_USERNAME")
	pass := os.Getenv("KEENETIC_PASSWORD")
	if ep == "" || user == "" || pass == "" {
		t.Skip("set KEENETIC_ENDPOINT/USERNAME/PASSWORD to run the live concurrency test")
	}
	c, err := New(ep, user, pass)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	const n = 24 // > Terraform's default -parallelism=10
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var out map[string]interface{}
			errs[i] = c.get(ctx, "/rci/show/rc/interface", &out)
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Errorf("concurrent get #%d failed: %v", i, e)
		}
	}
}
