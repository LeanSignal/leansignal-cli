package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/leansignal/leansignal-cli/internal/client"
	"github.com/leansignal/leansignal-cli/internal/output"
)

func newContextCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "context",
		Aliases: []string{"contexts", "ctx"},
		Short:   "Switch between tenants",
		Long: `A context pairs a tenant's API endpoint with an access token.

Log in once per tenant, then switch with 'leanctl context use <name>' — or
select one for a single command with the global --context flag.`,
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

	cmd.AddCommand(listCmd, useCmd, deleteCmd, currentCmd)

	return cmd
}
