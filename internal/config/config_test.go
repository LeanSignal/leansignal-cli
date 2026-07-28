package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/leansignal/leansignal-cli/internal/config"
)

func writeConfig(t *testing.T, body string, perm os.FileMode) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte(body), perm); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	return path
}

const sample = `current_context: acme
contexts:
  acme:
    tenant: acme
    api_url: https://acme-api.example.com
    token: lsp_secret
    user: you@example.com
    role: admin
  other:
    tenant: other
    api_url: https://other-api.example.com
    token: lsp_other
`

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	f, err := config.Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load on a missing file: %v", err)
	}

	if len(f.Contexts) != 0 {
		t.Errorf("expected no contexts, got %d", len(f.Contexts))
	}
}

// A config file holds a bearer token, so anything group- or world-readable
// must be refused rather than silently used.
func TestLoadRejectsWidePermissions(t *testing.T) {
	path := writeConfig(t, sample, 0o644)

	if _, err := config.Load(path); err == nil {
		t.Fatal("expected Load to reject a world-readable config")
	}
}

func TestSaveWritesPrivatePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.yaml")

	f, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	f.CurrentContext = "a"
	f.Contexts["a"] = &config.Context{Tenant: "a", APIURL: "https://a", Token: "lsp_a"}

	if err := f.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config permissions = %#o, want 0600", perm)
	}

	// Round-trip: the saved file must load cleanly under the same guard.
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if reloaded.Contexts["a"].Token != "lsp_a" {
		t.Errorf("token did not round-trip: %+v", reloaded.Contexts["a"])
	}
}

func TestResolvePrecedence(t *testing.T) {
	path := writeConfig(t, sample, 0o600)

	t.Run("file supplies the current context", func(t *testing.T) {
		s, err := config.Resolve(config.Overrides{ConfigPath: path})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}

		if s.Tenant != "acme" || s.Token != "lsp_secret" {
			t.Errorf("got tenant=%q token=%q", s.Tenant, s.Token)
		}

		if s.Output != "table" {
			t.Errorf("default output = %q, want table", s.Output)
		}
	})

	t.Run("context flag selects another context", func(t *testing.T) {
		s, err := config.Resolve(config.Overrides{ConfigPath: path, ContextName: "other"})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}

		if s.Token != "lsp_other" {
			t.Errorf("token = %q, want lsp_other", s.Token)
		}
	})

	t.Run("unknown context is an error", func(t *testing.T) {
		if _, err := config.Resolve(config.Overrides{ConfigPath: path, ContextName: "nope"}); err == nil {
			t.Fatal("expected an error for an unknown context")
		}
	})

	t.Run("env beats the file", func(t *testing.T) {
		t.Setenv("LEANCTL_TOKEN", "lsp_from_env")

		s, err := config.Resolve(config.Overrides{ConfigPath: path})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}

		if s.Token != "lsp_from_env" {
			t.Errorf("token = %q, want the environment value", s.Token)
		}
	})

	t.Run("flags beat env", func(t *testing.T) {
		t.Setenv("LEANCTL_API_URL", "https://from-env")

		s, err := config.Resolve(config.Overrides{ConfigPath: path, APIURL: "https://from-flag/"})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}

		if s.APIURL != "https://from-flag" {
			t.Errorf("api url = %q, want the flag value with the trailing slash trimmed", s.APIURL)
		}
	})
}

// LEANCTL_TOKEN plus LEANCTL_API_URL must be a complete credential, so CI needs
// no config file at all.
func TestResolveWorksWithoutAConfigFile(t *testing.T) {
	t.Setenv("LEANCTL_TOKEN", "lsp_ci")
	t.Setenv("LEANCTL_API_URL", "https://ci-api.example.com")

	s, err := config.Resolve(config.Overrides{ConfigPath: filepath.Join(t.TempDir(), "none.yaml")})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if err := s.RequireCredential(); err != nil {
		t.Fatalf("RequireCredential: %v", err)
	}
}

func TestRequireCredential(t *testing.T) {
	tests := []struct {
		name    string
		s       config.Settings
		wantErr bool
	}{
		{"https is fine", config.Settings{APIURL: "https://api.example.com", Token: "lsp_a"}, false},
		{"loopback http is fine", config.Settings{APIURL: "http://localhost:8080", Token: "lsp_a"}, false},
		{"remote http is refused", config.Settings{APIURL: "http://api.example.com", Token: "lsp_a"}, true},
		{"no token", config.Settings{APIURL: "https://api.example.com"}, true},
		{"no url", config.Settings{Token: "lsp_a"}, true},
		{"bad scheme", config.Settings{APIURL: "ftp://api.example.com", Token: "lsp_a"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.RequireCredential()
			if (err != nil) != tc.wantErr {
				t.Errorf("RequireCredential() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
