package cli

import (
	"net/url"

	"github.com/spf13/cobra"

	"github.com/leansignal/leansignal-cli/internal/client"
	"github.com/leansignal/leansignal-cli/internal/output"
)

func newSearchCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "search <term>",
		Short:   "Search demands, dashboards, agents, alerts, and channels by name",
		Example: `  leanctl search cpu`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGet(cmd, f, "/search", url.Values{"q": {args[0]}},
				func(r client.SearchResult, _ bool) *output.Table {
					t := output.NewTable("KIND", "NAME", "ID")
					t.Empty = "Nothing matched."
					t.IDColumn = 2

					for _, group := range []struct {
						kind  string
						items []client.SearchItem
					}{
						{"demand", r.Demands},
						{"dashboard", r.Dashboards},
						{"agent", r.Agents},
						{"alert_rule", r.AlertRules},
						{"channel", r.NotificationChannels},
					} {
						for _, item := range group.items {
							t.Add(group.kind, item.Name, item.ID)
						}
					}

					return t
				})
		},
	}

	return cmd
}
