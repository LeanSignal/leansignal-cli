// Package client is leanctl's HTTP client for the lean-api REST surface.
//
// It authenticates with a personal access token in an Authorization header —
// never in a query string, where it would land in access logs and shell
// history. Responses come back as raw bytes so that `-o json` can print exactly
// what the server said, with typed decoding layered on top only where a command
// needs to build a table.
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/leansignal/lean-cli/internal/config"
)

// APIPrefix is the versioned base path every resource route hangs off.
const APIPrefix = "/api/v1"

// maxBody caps a response we buffer in memory (proxied query results can be
// large, but not unbounded).
const maxBody = 64 << 20 // 64 MiB

// Client talks to one tenant's lean-api.
type Client struct {
	baseURL   string
	token     string
	userAgent string
	verbose   bool
	http      *http.Client
	errOut    io.Writer
}

// New builds a client from resolved settings.
func New(s *config.Settings, userAgent string) (*Client, error) {
	if err := s.RequireCredential(); err != nil {
		return nil, err
	}

	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:   true,
		MaxIdleConnsPerHost: 4,
	}

	return &Client{
		baseURL:   s.APIURL,
		token:     s.Token,
		userAgent: userAgent,
		verbose:   s.Verbose,
		http:      &http.Client{Timeout: s.Timeout, Transport: transport},
		errOut:    os.Stderr,
	}, nil
}

// Request is one API call.
type Request struct {
	Method string
	// Path is relative to /api/v1 (e.g. "/demands"). A path starting with
	// "/api/" or "/internal/" is used verbatim.
	Path  string
	Query url.Values
	Body  []byte
	// ContentType defaults to application/json when Body is set.
	ContentType string
	// Accept defaults to application/json.
	Accept string
}

// Response is a successful (2xx) API response.
type Response struct {
	Status      int
	Body        []byte
	ContentType string
}

// Do performs the request, returning an *APIError for any non-2xx status.
func (c *Client) Do(ctx context.Context, r Request) (*Response, error) {
	endpoint := c.baseURL + normalizePath(r.Path)
	if len(r.Query) > 0 {
		endpoint += "?" + r.Query.Encode()
	}

	var body io.Reader
	if len(r.Body) > 0 {
		body = bytes.NewReader(r.Body)
	}

	req, err := http.NewRequestWithContext(ctx, r.Method, endpoint, body)
	if err != nil {
		return nil, &UsageError{Msg: err.Error()}
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", orDefault(r.Accept, "application/json"))

	if len(r.Body) > 0 {
		req.Header.Set("Content-Type", orDefault(r.ContentType, "application/json"))
	}

	c.trace("→ %s %s", r.Method, endpoint)

	start := time.Now()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &NetworkError{Op: fmt.Sprintf("%s %s", r.Method, redactURL(endpoint)), Err: err}
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, &NetworkError{Op: "reading response body", Err: err}
	}

	c.trace("← %d %s (%d bytes, %s)", resp.StatusCode, resp.Status, len(raw), time.Since(start).Round(time.Millisecond))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, parseAPIError(resp.StatusCode, r.Method, r.Path, raw)
	}

	return &Response{
		Status:      resp.StatusCode,
		Body:        raw,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

// Get issues a GET and returns the raw body.
func (c *Client) Get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	resp, err := c.Do(ctx, Request{Method: http.MethodGet, Path: path, Query: q})
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}

// Post issues a POST with a JSON body.
func (c *Client) Post(ctx context.Context, path string, q url.Values, body []byte) ([]byte, error) {
	resp, err := c.Do(ctx, Request{Method: http.MethodPost, Path: path, Query: q, Body: body})
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}

// Put issues a PUT with a JSON body.
func (c *Client) Put(ctx context.Context, path string, q url.Values, body []byte) ([]byte, error) {
	resp, err := c.Do(ctx, Request{Method: http.MethodPut, Path: path, Query: q, Body: body})
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}

// Delete issues a DELETE.
func (c *Client) Delete(ctx context.Context, path string, q url.Values) error {
	_, err := c.Do(ctx, Request{Method: http.MethodDelete, Path: path, Query: q})

	return err
}

// GetInto issues a GET and decodes the body into out, also returning the raw
// bytes so the caller can print the server's own JSON verbatim.
func (c *Client) GetInto(ctx context.Context, path string, q url.Values, out any) ([]byte, error) {
	raw, err := c.Get(ctx, path, q)
	if err != nil {
		return nil, err
	}

	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return raw, fmt.Errorf("decoding %s response: %w", path, err)
		}
	}

	return raw, nil
}

// BaseURL returns the tenant API origin this client talks to.
func (c *Client) BaseURL() string { return c.baseURL }

func (c *Client) trace(format string, args ...any) {
	if !c.verbose {
		return
	}

	fmt.Fprintf(c.errOut, "· "+format+"\n", args...)
}

// normalizePath prefixes /api/v1 unless the caller passed an absolute API path.
func normalizePath(p string) string {
	if p == "" {
		return APIPrefix
	}

	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/internal/") ||
		p == "/health" || p == "/healthz" {
		return p
	}

	return APIPrefix + p
}

// redactURL strips any query string before an error message reaches a log.
// leanctl never puts a token in a URL, but a proxied query can carry data the
// user would rather not see echoed.
func redactURL(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i] + "?…"
	}

	return u
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
}
