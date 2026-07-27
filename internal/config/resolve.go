package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultCCBaseURL is the control-center that resolves a tenant slug to its API
// host. Override with LEANCTL_CC_URL.
const DefaultCCBaseURL = "https://cc.leansignal.io"

// defaultResolveToken is the coarse, public token control-center requires on
// /resolve_tenant. It is not a credential — the web app ships the same value in
// its JavaScript bundle. Override with LEANCTL_RESOLVE_TOKEN.
const defaultResolveToken = "fad77809-e6c4-49b0-a508-0e1e469e6553"

// tenantSlug guards the value we interpolate into the resolve URL.
var tenantSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ResolveTenant asks control-center which API host serves a tenant, the same
// way the web app does at boot (see lean-ui/app/lib/preflight.ts):
//
//	GET <cc>/resolve_tenant?tenant=<slug>&aat=<token> -> {"api_url": "host"}
//
// It returns a normalized https origin.
func ResolveTenant(ctx context.Context, ccBase, resolveToken, tenant string, timeout time.Duration) (string, error) {
	tenant = strings.ToLower(strings.TrimSpace(tenant))
	if !tenantSlug.MatchString(tenant) {
		return "", fmt.Errorf("invalid tenant %q: expected a slug like 'petkopuma'", tenant)
	}

	if ccBase == "" {
		ccBase = DefaultCCBaseURL
	}

	if resolveToken == "" {
		resolveToken = defaultResolveToken
	}

	endpoint := fmt.Sprintf("%s/resolve_tenant?tenant=%s&aat=%s",
		strings.TrimRight(ccBase, "/"), url.QueryEscape(tenant), url.QueryEscape(resolveToken))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return "", fmt.Errorf("control center unreachable at %s: %w", ccBase, err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("unknown tenant %q", tenant)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("control center returned HTTP %d resolving tenant %q", resp.StatusCode, tenant)
	}

	var body struct {
		APIURL string `json:"api_url"`
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", fmt.Errorf("reading resolve response: %w", err)
	}

	if err := json.Unmarshal(raw, &body); err != nil {
		return "", fmt.Errorf("decoding resolve response: %w", err)
	}

	if body.APIURL == "" {
		return "", fmt.Errorf("control center returned no api_url for tenant %q", tenant)
	}

	origin := strings.TrimRight(strings.TrimSpace(body.APIURL), "/")
	if !strings.HasPrefix(origin, "http://") && !strings.HasPrefix(origin, "https://") {
		origin = "https://" + origin
	}

	return origin, nil
}
