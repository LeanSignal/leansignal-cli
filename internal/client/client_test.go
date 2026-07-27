package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/leansignal/lean-cli/internal/client"
	"github.com/leansignal/lean-cli/internal/config"
)

func newTestClient(t *testing.T, h http.HandlerFunc) (*client.Client, *httptest.Server) {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	// httptest serves plain HTTP on loopback, which RequireCredential allows.
	c, err := client.New(&config.Settings{APIURL: srv.URL, Token: "lsp_test"}, "leanctl-test")
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	return c, srv
}

// The token must travel in the Authorization header and never in the URL.
func TestTokenTravelsInTheHeaderOnly(t *testing.T) {
	var (
		gotAuth string
		gotURL  string
	)

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotURL = r.URL.String()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"meta":{"page":1,"per_page":20,"total":0}}`))
	})

	if _, err := c.Get(context.Background(), "/demands", url.Values{"search": {"x"}}); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if gotAuth != "Bearer lsp_test" {
		t.Errorf("Authorization = %q", gotAuth)
	}

	if strings.Contains(gotURL, "lsp_test") {
		t.Errorf("token leaked into the URL: %s", gotURL)
	}

	if !strings.HasPrefix(gotURL, "/api/v1/demands") {
		t.Errorf("path = %q, want the /api/v1 prefix applied", gotURL)
	}
}

func TestErrorEnvelopeDecoding(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"insufficient_scope","message":"token is read-only"}`))
	})

	_, err := c.Get(context.Background(), "/demands", nil)
	if err == nil {
		t.Fatal("expected an error")
	}

	var apiErr *client.APIError
	if !errorsAs(err, &apiErr) {
		t.Fatalf("error type = %T, want *client.APIError", err)
	}

	if apiErr.Status != http.StatusForbidden || apiErr.Code != "insufficient_scope" {
		t.Errorf("got status=%d code=%q", apiErr.Status, apiErr.Code)
	}

	if client.ExitCode(err) != client.ExitAuth {
		t.Errorf("exit code = %d, want %d", client.ExitCode(err), client.ExitAuth)
	}

	if apiErr.Hint() == "" {
		t.Error("insufficient_scope should carry an actionable hint")
	}
}

// A body that is not the error envelope (an ingress HTML page, say) must still
// produce a usable message rather than an empty one.
func TestErrorFallbackForNonEnvelopeBodies(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	})

	_, err := c.Get(context.Background(), "/demands", nil)
	if err == nil {
		t.Fatal("expected an error")
	}

	if err.Error() == "" {
		t.Error("error message is empty")
	}

	if client.ExitCode(err) != client.ExitServer {
		t.Errorf("exit code = %d, want %d", client.ExitCode(err), client.ExitServer)
	}
}

func TestListFollowsEveryPageWithAll(t *testing.T) {
	const total = 5

	var requests int

	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++

		page := r.URL.Query().Get("page")

		items := []map[string]string{}

		if page == "1" {
			for i := range total {
				items = append(items, map[string]string{"id": string(rune('a' + i))})
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": items,
			"meta":  map[string]int{"page": 1, "per_page": 200, "total": total},
		})
	})

	type item struct {
		ID string `json:"id"`
	}

	page, raw, err := client.List[item](context.Background(), c, "/demands", client.ListOptions{All: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(page.Items) != total {
		t.Errorf("items = %d, want %d", len(page.Items), total)
	}

	if requests != 1 {
		t.Errorf("requests = %d, want 1 (the first page already covered the total)", requests)
	}

	// --all must still emit one well-formed envelope for -o json.
	var envelope struct {
		Items []item `json:"items"`
	}

	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("paged output is not a valid envelope: %v", err)
	}
}

func TestNetworkFailureExitCode(t *testing.T) {
	c, srv := newTestClient(t, func(http.ResponseWriter, *http.Request) {})
	srv.Close()

	_, err := c.Get(context.Background(), "/demands", nil)
	if err == nil {
		t.Fatal("expected an error against a closed server")
	}

	if client.ExitCode(err) != client.ExitNetwork {
		t.Errorf("exit code = %d, want %d", client.ExitCode(err), client.ExitNetwork)
	}
}

// errorsAs keeps the test readable without importing errors in every case.
func errorsAs(err error, target **client.APIError) bool {
	for err != nil {
		if e, ok := err.(*client.APIError); ok { //nolint:errorlint // walking manually is the point
			*target = e

			return true
		}

		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}

		err = u.Unwrap()
	}

	return false
}
