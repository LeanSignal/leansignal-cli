package cli

import (
	"github.com/spf13/cobra"

	"github.com/leansignal/lean-cli/internal/client"
	"github.com/leansignal/lean-cli/internal/output"
)

func newAuditCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "audit",
		Aliases: []string{"audit-log"},
		Short:   "Read the audit log (admin)",
		Long: `The audit log is an append-only trail of changes: who created, updated, or
deleted what, and when. Admin role required.`,
	}

	var (
		opts       listFlags
		objectType string
		action     string
		actor      string
	)

	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List audit entries",
		Example: `  leanctl audit list --object-type demand --action deleted --all`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o := opts.options()
			o.Extra = queryOf(
				"object_type", objectType,
				"action", action,
				"actor_email", actor,
			)

			return runList(cmd, f, "/audit-log", o,
				func(items []client.AuditEntry, wide bool) *output.Table {
					headers := []string{"WHEN", "ACTOR", "ACTION", "TYPE", "OBJECT"}
					if wide {
						headers = append(headers, "OBJECT ID")
					}

					t := output.NewTable(headers...)
					t.Empty = "No audit entries."

					for _, e := range items {
						if wide {
							t.Add(e.CreatedAt, e.ActorEmail, e.Action, e.ObjectType, e.ObjectName, e.ObjectID)

							continue
						}

						t.Add(e.CreatedAt, e.ActorEmail, e.Action, e.ObjectType, e.ObjectName)
					}

					return t
				})
		},
	}

	opts.bind(listCmd)
	listCmd.Flags().StringVar(&objectType, "object-type", "", "filter by object type, e.g. demand, dashboard")
	listCmd.Flags().StringVar(&action, "action", "", "filter by action: created, updated, deleted")
	listCmd.Flags().StringVar(&actor, "actor", "", "filter by actor email")

	cmd.AddCommand(listCmd)

	return cmd
}
