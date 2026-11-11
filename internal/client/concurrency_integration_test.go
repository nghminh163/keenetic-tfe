package client

import (
	"context"
	"os"
	"sync"
	"testing"
)

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
