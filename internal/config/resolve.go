package config

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// Tenant endpoints are regional (acme-api.eu11.leansignal.io) and tenants can
// move between regions, so a stored URL is a cache, not a fact. The one
// region-less host is control-center, whose /resolve_tenant answers "which API
// host serves this tenant right now" — the same lookup the web app performs at
// boot and the agent performs at startup.
//
// This is deliberately the ONLY control-center endpoint the CLI ever calls
// (TestControlCenterIsResolveOnly enforces it): resolution needs a region-less
// front door by definition, while everything else the CLI does works against
// the tenant API alone.

// DefaultCCBaseURL is the control-center front door. Override with
// LEANCTL_CC_URL.
const DefaultCCBaseURL = "https://cc.leansignal.io"

// defaultResolveToken is the coarse, public token /resolve_tenant requires. It
// is not a credential — the web app ships the same value in its JavaScript
// bundle for every visitor. Override with LEANCTL_RESOLVE_TOKEN.
const defaultResolveToken = "fad77809-e6c4-49b0-a508-0e1e469e6553"

// tenantSlugRE guards the value interpolated into the resolve URL.
var tenantSlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// CCBaseURL returns the control-center base, honouring the env override.
func CCBaseURL() string {
	if v := os.Getenv(EnvPrefix + "_CC_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}

	return DefaultCCBaseURL
}

// ResolveTenant asks control-center which API host currently serves a tenant:
//
//	GET <cc>/resolve_tenant?tenant=<slug>&aat=<token> -> {"api_url": "host"}
//
// It returns a normalized https origin.
func ResolveTenant(ctx context.Context, tenant string, timeout time.Duration) (string, error) {
	tenant = strings.ToLower(strings.TrimSpace(tenant))
	if !tenantSlugRE.MatchString(tenant) {
		return "", fmt.Errorf("invalid tenant %q: expected a slug like %q", tenant, "acme")
	}

	resolveToken := os.Getenv(EnvPrefix + "_RESOLVE_TOKEN")
	if resolveToken == "" {
		resolveToken = defaultResolveToken
	}

	endpoint := fmt.Sprintf("%s/resolve_tenant?tenant=%s&aat=%s",
		CCBaseURL(), url.QueryEscape(tenant), url.QueryEscape(resolveToken))

	// The endpoint is not attacker-taintable: the host comes from operator
	// configuration (LEANCTL_CC_URL or the built-in default), the slug is
	// regex-validated above, and both query values are URL-escaped.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody) //nolint:gosec // G704: see above
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/json")

	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	resp, err := (&http.Client{Timeout: timeout}).Do(req) //nolint:gosec // G704: host is operator config, slug validated
	if err != nil {
		return "", fmt.Errorf("control center unreachable at %s: %w", CCBaseURL(), err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("unknown tenant %q", tenant)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("control center answered HTTP %d resolving tenant %q", resp.StatusCode, tenant)
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

	origin := strings.TrimRight(strings.TrimSpace(body.APIURL), "/")
	if origin == "" {
		return "", fmt.Errorf("control center returned no api_url for tenant %q", tenant)
	}

	if !strings.HasPrefix(origin, "http://") && !strings.HasPrefix(origin, "https://") {
		origin = "https://" + origin
	}

	return origin, nil
}
