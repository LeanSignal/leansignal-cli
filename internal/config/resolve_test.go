package config_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/leansignal/leansignal-cli/internal/config"
)

// newCC stands in for control-center's /resolve_tenant.
func newCC(t *testing.T, handler http.HandlerFunc) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("LEANCTL_CC_URL", srv.URL)
}

func TestResolveTenant(t *testing.T) {
	newCC(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/resolve_tenant" {
			t.Errorf("path = %q", r.URL.Path)
		}

		// The public resolve token must travel as a query param, matching the
		// contract the web app uses.
		if r.URL.Query().Get("tenant") != "acme" || r.URL.Query().Get("aat") == "" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}

		// CC answers a bare host; the client must normalize it to an origin.
		_ = json.NewEncoder(w).Encode(map[string]string{"api_url": "acme-api.eu11.example.com"})
	})

	got, err := config.ResolveTenant(t.Context(), "acme", time.Second)
	if err != nil {
		t.Fatalf("ResolveTenant: %v", err)
	}

	if got != "https://acme-api.eu11.example.com" {
		t.Errorf("got %q", got)
	}
}

func TestResolveTenantUnknown(t *testing.T) {
	newCC(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })

	_, err := config.ResolveTenant(t.Context(), "ghost", time.Second)
	if err == nil || !strings.Contains(err.Error(), "unknown tenant") {
		t.Fatalf("err = %v, want unknown tenant", err)
	}
}

// A slug is interpolated into a URL, so anything not slug-shaped is refused
// before any request is made.
func TestResolveTenantRejectsNonSlugs(t *testing.T) {
	newCC(t, func(http.ResponseWriter, *http.Request) {
		t.Error("the resolver must not be called for an invalid slug")
	})

	// "Acme" is absent on purpose: input is lowercased before validation, so
	// mixed case is accepted, not rejected.
	for _, bad := range []string{"", "a/b", "a b", "acme.example.com?x=1", "-acme"} {
		if _, err := config.ResolveTenant(t.Context(), bad, time.Second); err == nil {
			t.Errorf("slug %q was accepted", bad)
		}
	}
}
