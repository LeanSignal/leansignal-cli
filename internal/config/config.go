// Package config loads, resolves, and persists leanctl's configuration.
//
// The config file is a kubectl-style set of named contexts, one per tenant:
//
//	current_context: acme
//	contexts:
//	  acme:
//	    tenant: acme
//	    api_url: https://acme-api.eu11.leansignal.io
//	    token: lsp_…
//
// It holds a bearer token, so it is written 0600 inside a 0700 directory and
// leanctl refuses to read it when the permissions are wider than that.
//
// Precedence is the conventional one: explicit flags beat environment variables
// beat the config file. LEANCTL_TOKEN alone is a complete credential — that is
// the CI mode, where no config file exists at all.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// EnvPrefix is the prefix for every environment variable leanctl reads.
const EnvPrefix = "LEANCTL"

// DefaultTimeout bounds a single API request.
const DefaultTimeout = 30 * time.Second

// Context is one tenant's endpoint and credential.
type Context struct {
	Tenant string `mapstructure:"tenant" yaml:"tenant"`
	// APIURL is a CACHE of the tenant's current regional endpoint when Resolve
	// is true, and a user-pinned endpoint when it is false. Tenants can move
	// between regions, so resolve-originated contexts re-resolve and rewrite
	// this on a connection failure.
	APIURL string `mapstructure:"api_url" yaml:"api_url"`
	// Resolve records how APIURL was obtained: true = resolved via
	// control-center (re-resolution allowed), false = pinned with --api-url.
	Resolve bool   `mapstructure:"resolve" yaml:"resolve,omitempty"`
	Token   string `mapstructure:"token" yaml:"token"`
	// User and Role are cached from /auth/me at login time for display only.
	// They are never trusted for access decisions — the server decides.
	User string `mapstructure:"user" yaml:"user,omitempty"`
	Role string `mapstructure:"role" yaml:"role,omitempty"`
}

// File is the on-disk config document.
type File struct {
	CurrentContext string              `mapstructure:"current_context" yaml:"current_context"`
	Contexts       map[string]*Context `mapstructure:"contexts"        yaml:"contexts"`

	path string
}

// Path returns the file this config was loaded from (or would be written to).
func (f *File) Path() string { return f.path }

// Overrides carries the values supplied by global flags. Empty fields mean
// "not set on the command line" and fall through to env, then to the file.
type Overrides struct {
	ConfigPath  string
	ContextName string
	APIURL      string
	Token       string
	Output      string
	Timeout     time.Duration
	Verbose     bool
	NoColor     bool
}

// Settings is the fully resolved configuration a command actually runs with.
type Settings struct {
	ContextName string
	Tenant      string
	APIURL      string
	Token       string
	Output      string
	Timeout     time.Duration
	Verbose     bool
	NoColor     bool

	File *File
}

// ErrNoContext means no credential could be resolved from flags, env, or file.
var ErrNoContext = errors.New(
	"no context configured — run 'leanctl auth login --tenant <tenant>'" +
		" or set LEANCTL_TOKEN and LEANCTL_API_URL")

// DefaultPath is $XDG_CONFIG_HOME/leanctl/config.yaml, falling back to
// ~/.config/leanctl/config.yaml.
func DefaultPath() string {
	if p := os.Getenv(EnvPrefix + "_CONFIG"); p != "" {
		return p
	}

	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(".leanctl", "config.yaml")
		}

		dir = filepath.Join(home, ".config")
	}

	return filepath.Join(dir, "leanctl", "config.yaml")
}

// Load reads the config file. A missing file is not an error: it yields an
// empty config, so `LEANCTL_TOKEN=… leanctl demand list` works on a fresh box.
func Load(path string) (*File, error) {
	if path == "" {
		path = DefaultPath()
	}

	f := &File{Contexts: map[string]*Context{}, path: path}

	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	// The file holds a bearer token. Anything group- or world-readable is a
	// leak waiting to happen, so refuse rather than silently continue.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf(
			"config file %s has permissions %#o; it contains an access token and must not be readable by others"+
				" (fix with: chmod 600 %s)", path, perm, path)
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	if err := v.Unmarshal(f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	if f.Contexts == nil {
		f.Contexts = map[string]*Context{}
	}

	f.path = path

	return f, nil
}

// Save writes the config atomically with 0600 permissions.
func (f *File) Save() error {
	if f.path == "" {
		f.path = DefaultPath()
	}

	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(f.path), err)
	}

	body, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	// Write to a sibling temp file and rename, so an interrupted write can
	// never leave a truncated config (and never a token-less half-file).
	tmp, err := os.CreateTemp(filepath.Dir(f.path), ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("creating temp config: %w", err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("securing temp config: %w", err)
	}

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("writing temp config: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp config: %w", err)
	}

	if err := os.Rename(tmp.Name(), f.path); err != nil {
		return fmt.Errorf("saving %s: %w", f.path, err)
	}

	return nil
}

// Resolve applies flag > env > file precedence and returns the settings the
// command should run with. It does not require a credential; callers that need
// one call Settings.RequireCredential.
func Resolve(o Overrides) (*Settings, error) {
	file, err := Load(o.ConfigPath)
	if err != nil {
		return nil, err
	}

	v := viper.New()
	v.SetEnvPrefix(EnvPrefix)
	v.AutomaticEnv()

	s := &Settings{
		ContextName: firstNonEmpty(o.ContextName, v.GetString("context"), v.GetString("profile"), file.CurrentContext),
		APIURL:      firstNonEmpty(o.APIURL, v.GetString("api_url")),
		Token:       firstNonEmpty(o.Token, v.GetString("token")),
		Output:      firstNonEmpty(o.Output, v.GetString("output"), "table"),
		Timeout:     o.Timeout,
		Verbose:     o.Verbose || v.GetBool("verbose"),
		NoColor:     o.NoColor || v.GetBool("no_color") || os.Getenv("NO_COLOR") != "",
		File:        file,
	}

	if s.Timeout <= 0 {
		s.Timeout = DefaultTimeout
	}

	// Fill the gaps from the selected context.
	if ctx, ok := file.Contexts[s.ContextName]; ok && ctx != nil {
		s.Tenant = ctx.Tenant
		s.APIURL = firstNonEmpty(s.APIURL, ctx.APIURL)
		s.Token = firstNonEmpty(s.Token, ctx.Token)
	} else if s.ContextName != "" && o.ContextName != "" {
		return nil, fmt.Errorf("context %q not found in %s", s.ContextName, file.Path())
	}

	s.APIURL = strings.TrimRight(s.APIURL, "/")

	return s, nil
}

// RequireCredential fails when no API URL or token is available, and rejects a
// plaintext endpoint outside loopback — a bearer token must not cross the
// network in the clear.
func (s *Settings) RequireCredential() error {
	if s.APIURL == "" || s.Token == "" {
		return ErrNoContext
	}

	u, err := url.Parse(s.APIURL)
	if err != nil {
		return fmt.Errorf("invalid api url %q: %w", s.APIURL, err)
	}

	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return fmt.Errorf(
			"refusing to send an access token over plain HTTP to %s — use https", u.Host)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid api url %q: expected http or https", s.APIURL)
	}

	return nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "[::1]"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}
