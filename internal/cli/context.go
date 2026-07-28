package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/leansignal/leansignal-cli/internal/client"
	"github.com/leansignal/leansignal-cli/internal/config"
	"github.com/leansignal/leansignal-cli/internal/output"
)

func newContextCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "context",
		Aliases: []string{"contexts", "ctx", "profile", "profiles"},
		Short:   "Switch between tenants (also answers to: profile)",
		Long: `A context — a profile, if that is the word you reach for — pairs one tenant's
API endpoint with an access token. Having several is normal: log in once per
tenant and each login saves its own entry.

Switch with 'leanctl context use <name>' (or 'leanctl profile use <name>' —
the AWS-style spelling works everywhere), or select one for a single command
with --context/--profile, or set LEANCTL_PROFILE in the environment.`,
	}

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List configured contexts",
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			settings, err := f.Settings()
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			file := settings.File

			names := make([]string, 0, len(file.Contexts))
			for name := range file.Contexts {
				names = append(names, name)
			}

			sort.Strings(names)

			return p.Emit(mustJSON(file), func() (*output.Table, error) {
				t := output.NewTable("CURRENT", "NAME", "TENANT", "API", "USER", "ROLE")
				t.Empty = "No contexts. Run 'leanctl auth login --tenant <tenant>'."
				t.IDColumn = 1

				for _, name := range names {
					c := file.Contexts[name]

					marker := ""
					if name == file.CurrentContext {
						marker = "*"
					}

					t.Add(marker, name, c.Tenant, c.APIURL, c.User, c.Role)
				}

				return t, nil
			})
		},
	}

	useCmd := &cobra.Command{
		Use:   "use <name>",
		Short: "Set the current context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := f.Settings()
			if err != nil {
				return err
			}

			file := settings.File

			if _, ok := file.Contexts[args[0]]; !ok {
				return client.Usage("context %q not found in %s", args[0], file.Path())
			}

			file.CurrentContext = args[0]

			if err := file.Save(); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Switched to context %q\n", args[0])

			return nil
		},
	}

	var yes bool

	deleteCmd := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a context and its stored token",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			settings, err := f.Settings()
			if err != nil {
				return err
			}

			file := settings.File

			if _, ok := file.Contexts[args[0]]; !ok {
				return client.Usage("context %q not found in %s", args[0], file.Path())
			}

			if err := confirm(cmd, yes, "delete context %q", args[0]); err != nil {
				return err
			}

			delete(file.Contexts, args[0])

			if file.CurrentContext == args[0] {
				file.CurrentContext = ""
			}

			if err := file.Save(); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Deleted context %q\n", args[0])
			fmt.Fprintln(cmd.ErrOrStderr(),
				"Note: the token was removed locally but is still valid on the server. "+
					"Revoke it with 'leanctl auth tokens revoke <id>'.")

			return nil
		},
	}

	deleteCmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	currentCmd := &cobra.Command{
		Use:   "current",
		Short: "Print the current context name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			settings, err := f.Settings()
			if err != nil {
				return err
			}

			if settings.ContextName == "" {
				return client.Usage("no current context set")
			}

			fmt.Fprintln(cmd.OutOrStdout(), settings.ContextName)

			return nil
		},
	}

	refreshCmd := &cobra.Command{
		Use:   "refresh [name]",
		Short: "Re-resolve a context's endpoint from control-center",
		Long: `Ask control-center where the context's tenant lives now and update the
stored endpoint.

Connection failures already trigger this automatically; refresh covers the case
where the old region still answers but no longer serves the tenant. Pinned
contexts (created with --api-url) are not resolvable — log in again to re-pin.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			settings, err := f.Settings()
			if err != nil {
				return err
			}

			name := settings.ContextName
			if len(args) == 1 {
				name = args[0]
			}

			file := settings.File

			cctx, ok := file.Contexts[name]
			if !ok || cctx == nil {
				return client.Usage("context %q not found in %s", name, file.Path())
			}

			if !cctx.Resolve || cctx.Tenant == "" {
				return client.Usage(
					"context %q pins its endpoint (--api-url); log in again to change it", name)
			}

			fresh, err := config.ResolveTenant(ctx, cctx.Tenant, settings.Timeout)
			if err != nil {
				return err
			}

			if fresh == cctx.APIURL {
				fmt.Fprintf(cmd.OutOrStdout(), "Context %q is current: %s\n", name, fresh)

				return nil
			}

			previous := cctx.APIURL
			cctx.APIURL = fresh

			if err := file.Save(); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Context %q: %s -> %s\n", name, previous, fresh)

			return nil
		},
	}

	var (
		addTenant  string
		addName    string
		tokenStdin bool
	)

	addCmd := &cobra.Command{
		Use:   "add",
		Short: "Add a profile without changing the current one",
		Long: `Add (or replace) one profile in the local config file.

This is 'auth login' with one difference: the current profile is left alone, so
adding a second tenant never silently repoints your next command. The write is
safe by construction — the whole file is parsed, one entry is upserted, and the
result is written atomically — so other profiles cannot be corrupted or lost.

The token is read from a no-echo prompt, or from stdin with --token-stdin. It
never belongs on the command line.`,
		Example: `  leanctl profile add --tenant acme
  cat token.txt | leanctl profile add --tenant acme --token-stdin`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return saveProfile(cmd, f, profileSpec{
				Tenant: addTenant, Name: addName, TokenStdin: tokenStdin, MakeCurrent: false,
			})
		},
	}

	addCmd.Flags().StringVar(&addTenant, "tenant", "",
		"tenant slug — the regional endpoint is resolved (and kept fresh) for you")
	addCmd.Flags().StringVar(&addName, "name", "", "profile name (default: the tenant slug)")
	addCmd.Flags().BoolVar(&tokenStdin, "token-stdin", false, "read the token from stdin instead of prompting")

	cmd.AddCommand(listCmd, useCmd, deleteCmd, currentCmd, refreshCmd, addCmd)

	return cmd
}
