package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/leansignal/leansignal-cli/internal/client"
	"github.com/leansignal/leansignal-cli/internal/config"
)

// A tenant that moved regions presents as its old endpoint refusing
// connections. The client must re-resolve once and complete the call against
// the new region — for any method, because a dial failure means nothing
// reached a server.
func TestDialFailureTriggersReresolve(t *testing.T) {
	var hits int

	newRegion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(newRegion.Close)

	oldRegion := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	oldURL := oldRegion.URL
	oldRegion.Close() // the old region no longer answers

	c, err := client.New(&config.Settings{APIURL: oldURL, Token: "lsp_test"}, "leanctl-test")
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	var resolved int

	c.SetReresolver(func(context.Context) (string, error) {
		resolved++

		return newRegion.URL, nil
	})

	if _, err := c.Post(context.Background(), "/demand", nil, []byte(`{"name":"x"}`)); err != nil {
		t.Fatalf("Post after failover: %v", err)
	}

	if resolved != 1 || hits != 1 {
		t.Errorf("resolved=%d hits=%d, want 1/1", resolved, hits)
	}

	if c.BaseURL() != newRegion.URL {
		t.Errorf("BaseURL = %q, want the new region", c.BaseURL())
	}
}

// An HTTP-level failure means a server WAS reached: the endpoint is correct and
// re-resolution must not fire — retrying a mutation there could apply it twice.
func TestHTTPErrorsDoNotReresolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c, err := client.New(&config.Settings{APIURL: srv.URL, Token: "lsp_test"}, "leanctl-test")
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	c.SetReresolver(func(context.Context) (string, error) {
		t.Error("re-resolve fired on an HTTP error")

		return "", nil
	})

	if _, err := c.Get(context.Background(), "/demands", nil); err == nil {
		t.Fatal("expected the 500 to surface")
	}
}

// A dead-but-accepting endpoint (a load balancer whose backend is gone) hangs
// instead of refusing. For an idempotent request that is still safe to retry
// against a fresh endpoint.
func TestTimeoutTriggersReresolveForGET(t *testing.T) {
	newRegion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(newRegion.Close)

	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(hang.Close)

	c, err := client.New(&config.Settings{
		APIURL: hang.URL, Token: "lsp_test", Timeout: 200 * time.Millisecond,
	}, "leanctl-test")
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	c.SetReresolver(func(context.Context) (string, error) { return newRegion.URL, nil })

	if _, err := c.Get(context.Background(), "/demands", nil); err != nil {
		t.Fatalf("GET after timeout failover: %v", err)
	}
}

// The same hang on a mutation must NOT retry: the hung server may have received
// and applied the request.
func TestTimeoutDoesNotRetryMutations(t *testing.T) {
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(hang.Close)

	c, err := client.New(&config.Settings{
		APIURL: hang.URL, Token: "lsp_test", Timeout: 200 * time.Millisecond,
	}, "leanctl-test")
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	c.SetReresolver(func(context.Context) (string, error) {
		t.Error("re-resolve fired for a timed-out mutation")

		return "", nil
	})

	if _, err := c.Post(context.Background(), "/demand", nil, []byte(`{}`)); err == nil {
		t.Fatal("expected the timeout to surface")
	}
}
