package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/leansignal/lean-cli/internal/client"
	"github.com/leansignal/lean-cli/internal/config"
	"github.com/leansignal/lean-cli/internal/output"
)

func newAuthCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate against a tenant",
		Long: `Manage the credential leanctl uses.

leanctl authenticates with a personal access token minted in the web app under
Preferences -> Access tokens. The token carries your identity and role: what you
can do in the UI is what you can do here.

Mint a token with the "write" scope to create or change anything; a "read" token
can list and inspect but every mutating call is refused.`,
	}

	cmd.AddCommand(
		newAuthLoginCommand(f),
		newAuthLogoutCommand(f),
		newAuthStatusCommand(f),
		newAuthTokenCommand(f),
	)

	return cmd
}

func newAuthLoginCommand(f *Factory) *cobra.Command {
	var (
		contextName string
		tokenStdin  bool
		ccURL       string
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store a personal access token for a tenant",
		Long: `Resolve a tenant's API endpoint, verify a personal access token against it,
and save both as a named context.

The token is read from a no-echo prompt by default. In CI, either pipe it in
with --token-stdin or skip login entirely and set LEANCTL_TOKEN together with
LEANCTL_API_URL.`,
		Example: `  leanctl auth login --tenant petkopuma
  cat token.txt | leanctl auth login --tenant petkopuma --token-stdin
  leanctl auth login --tenant dev --api-url http://localhost:8080`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			settings, err := f.Settings()
			if err != nil {
				return err
			}

			tenant, apiURL := settings.Tenant, settings.APIURL

			if tenant == "" && apiURL == "" {
				return client.Usage("--tenant is required (or pass --api-url directly)")
			}

			if apiURL == "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "Resolving tenant %q…\n", tenant)

				apiURL, err = config.ResolveTenant(ctx,
					firstNonEmpty(ccURL, os.Getenv("LEANCTL_CC_URL"), config.DefaultCCBaseURL),
					os.Getenv("LEANCTL_RESOLVE_TOKEN"),
					tenant, settings.Timeout)
				if err != nil {
					return err
				}
			}

			token, err := readLoginToken(tokenStdin, settings.Token)
			if err != nil {
				return err
			}

			if !strings.HasPrefix(token, "lsp_") {
				return client.Usage(
					"that does not look like a LeanSignal access token (expected an lsp_ prefix)")
			}

			// Verify before saving, so a mistyped token never lands on disk.
			probe := &config.Settings{APIURL: strings.TrimRight(apiURL, "/"), Token: token, Timeout: settings.Timeout}

			api, err := client.New(probe, "leanctl-login")
			if err != nil {
				return err
			}

			var me client.Me
			if _, err := api.GetInto(ctx, "/auth/me", nil, &me); err != nil {
				return err
			}

			name := contextName
			if name == "" {
				name = firstNonEmpty(tenant, me.TenantShortName, me.TenantName)
			}

			if name == "" {
				return client.Usage("could not derive a context name; pass --context")
			}

			file := settings.File
			file.Contexts[name] = &config.Context{
				Tenant: firstNonEmpty(tenant, me.TenantShortName, me.TenantName),
				APIURL: probe.APIURL,
				Token:  token,
				User:   me.Email,
				Role:   me.Role,
			}
			file.CurrentContext = name

			if err := file.Save(); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s as %s (%s)\n", probe.APIURL, me.Email, me.Role)
			fmt.Fprintf(cmd.OutOrStdout(), "Context %q saved to %s\n", name, file.Path())

			return nil
		},
	}

	fs := cmd.Flags()
	// The endpoint comes from the global --tenant / --api-url flags; --name is
	// what the saved context is called (the global --context selects one).
	fs.StringVar(&contextName, "name", "", "name for the saved context (default: tenant slug)")
	fs.BoolVar(&tokenStdin, "token-stdin", false, "read the token from stdin instead of prompting")
	fs.StringVar(&ccURL, "cc-url", "", "control-center base URL (default "+config.DefaultCCBaseURL+")")

	return cmd
}

func readLoginToken(fromStdin bool, envToken string) (string, error) {
	if fromStdin {
		return readStdinLine()
	}

	if envToken != "" {
		return envToken, nil
	}

	return promptSecret("Personal access token: ")
}

func newAuthLogoutCommand(f *Factory) *cobra.Command {
	var (
		yes    bool
		revoke bool
	)

	cmd := &cobra.Command{
		Use:   "logout [context]",
		Short: "Remove a stored credential",
		Long: `Delete a context's stored token.

By default the token is only removed locally and stays valid on the server. Pass
--revoke to also revoke it, which is what you want if the machine or the token
may have been exposed.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := f.Settings()
			if err != nil {
				return err
			}

			name := settings.ContextName
			if len(args) == 1 {
				name = args[0]
			}

			file := settings.File

			if _, ok := file.Contexts[name]; !ok {
				return client.Usage("context %q not found in %s", name, file.Path())
			}

			if err := confirm(cmd, yes, "log out of context %q", name); err != nil {
				return err
			}

			if revoke {
				if err := revokeCurrentToken(cmd, f); err != nil {
					return fmt.Errorf("revoking token: %w", err)
				}

				fmt.Fprintln(cmd.ErrOrStderr(), "Token revoked on the server.")
			}

			delete(file.Contexts, name)

			if file.CurrentContext == name {
				file.CurrentContext = ""

				for other := range file.Contexts {
					file.CurrentContext = other

					break
				}
			}

			if err := file.Save(); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Logged out of %q\n", name)

			return nil
		},
	}

	cmd.Flags().BoolVar(&revoke, "revoke", false, "also revoke the token on the server")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

// revokeCurrentToken finds the active token by its prefix and revokes it.
func revokeCurrentToken(cmd *cobra.Command, f *Factory) error {
	ctx, cancel := cmdContext(cmd)
	defer cancel()

	settings, err := f.Settings()
	if err != nil {
		return err
	}

	api, err := f.Client()
	if err != nil {
		return err
	}

	var list struct {
		Items []client.APIToken `json:"items"`
	}

	if _, err := api.GetInto(ctx, "/mcp_tokens", nil, &list); err != nil {
		return err
	}

	prefix := settings.Token
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}

	for _, t := range list.Items {
		if t.TokenPrefix == prefix {
			return api.Delete(ctx, "/mcp_tokens/"+t.ID, nil)
		}
	}

	return fmt.Errorf("the active token was not found on the server (already revoked?)")
}

func newAuthStatusCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show who you are and where you are pointed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings, err := f.Settings()
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			ctx, cancel := cmdContext(cmd)
			defer cancel()

			api, err := f.Client()
			if err != nil {
				return err
			}

			var me client.Me

			raw, err := api.GetInto(ctx, "/auth/me", nil, &me)
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				t := output.NewTable("FIELD", "VALUE")
				t.Add("context", settings.ContextName)
				t.Add("api", api.BaseURL())
				t.Add("tenant", firstNonEmpty(me.TenantShortName, me.TenantName, settings.Tenant))
				t.Add("user", me.Email)
				t.Add("role", me.Role)
				t.Add("token", maskToken(settings.Token))
				t.Add("config", settings.File.Path())

				return t, nil
			})
		},
	}

	return cmd
}

func newAuthTokenCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tokens",
		Aliases: []string{"token"},
		Short:   "Manage your personal access tokens",
	}

	var listOpts listFlags

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List your access tokens",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			api, err := f.Client()
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			var list struct {
				Items []client.APIToken `json:"items"`
			}

			raw, err := api.GetInto(ctx, "/mcp_tokens", nil, &list)
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				t := output.NewTable("ID", "NAME", "PREFIX", "SCOPES", "ROLE", "LAST USED", "EXPIRES")
				t.Empty = "No access tokens."

				for _, tok := range list.Items {
					t.Add(tok.ID, tok.Name, tok.TokenPrefix, tok.Scopes, tok.Role, tok.LastUsedAt, tok.ExpiresAt)
				}

				return t, nil
			})
		},
	}

	listOpts.bind(listCmd)

	var (
		tokenName string
		expiresIn string
		scopes    []string
	)

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Mint a new access token",
		Long: `Mint a personal access token.

The secret is printed once and never again — store it immediately. A token
inherits your current role and can never exceed it; scopes only narrow it
further.`,
		Example: `  leanctl auth tokens create --name laptop --scope read --scope write --expires-in 90d`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			if tokenName == "" {
				return client.Usage("--name is required")
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			body := map[string]any{"name": tokenName, "expires_in": expiresIn}
			if len(scopes) > 0 {
				body["scopes"] = scopes
			}

			raw, err := api.Post(ctx, "/mcp_tokens", nil, mustJSON(body))
			if err != nil {
				return err
			}

			if p.Structured() {
				return p.Emit(raw, nil)
			}

			var created client.APIToken
			if err := jsonUnmarshal(raw, &created); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", created.Token)
			fmt.Fprintln(cmd.ErrOrStderr(), "Store this token now — it will not be shown again.")

			return nil
		},
	}

	createCmd.Flags().StringVar(&tokenName, "name", "", "human-readable token name (required)")
	createCmd.Flags().StringVar(&expiresIn, "expires-in", "90d", "expiry window, e.g. 30d, 90d, 720h (empty = never)")
	createCmd.Flags().StringArrayVar(&scopes, "scope", nil, "scope to grant: read, write (repeatable)")

	var yes bool

	revokeCmd := &cobra.Command{
		Use:     "revoke <id>",
		Aliases: []string{"delete", "rm"},
		Short:   "Revoke an access token",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			if err := confirm(cmd, yes, "revoke token %s", args[0]); err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			if err := api.Delete(ctx, "/mcp_tokens/"+args[0], nil); err != nil {
				return err
			}

			return emitAction(f, nil, "Token %s revoked.", args[0])
		},
	}

	revokeCmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	cmd.AddCommand(listCmd, createCmd, revokeCmd)

	return cmd
}

// maskToken shows only enough of a token to tell two apart.
func maskToken(token string) string {
	if token == "" {
		return "-"
	}

	if len(token) <= 8 {
		return "****"
	}

	return token[:8] + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}
