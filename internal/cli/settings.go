package cli

import (
	"github.com/spf13/cobra"

	"github.com/leansignal/lean-cli/internal/client"
	"github.com/leansignal/lean-cli/internal/output"
)

func newSettingsCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Read and change tenant settings (admin)",
		Long: `Tenant-level knobs. Admin role required.

purge_grace is how long a deleted filter's data survives before the purge worker
removes it — the window in which un-demanding something can still be undone.`,
	}

	getCmd := &cobra.Command{
		Use:   "get",
		Short: "Show the tenant settings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGet(cmd, f, "/settings", nil, func(s client.Settings, _ bool) *output.Table {
				t := output.NewTable("SETTING", "VALUE", "DEFAULT")
				t.Add("purge_grace", s.PurgeGrace, s.PurgeGraceDefault)

				if s.PurgeGraceIsDefault {
					t.Add("", "(using the default)", "")
				}

				return t
			})
		},
	}

	var purgeGrace string

	setCmd := &cobra.Command{
		Use:     "set",
		Short:   "Change a tenant setting",
		Example: `  leanctl settings set --purge-grace 48h`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			if purgeGrace == "" {
				return client.Usage("nothing to set — pass --purge-grace")
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			body := mustJSON(map[string]any{"purge_grace": purgeGrace})

			raw, err := api.Put(ctx, "/settings", nil, body)
			if err != nil {
				return err
			}

			return emitAction(f, raw, "Set purge_grace to %s.", purgeGrace)
		},
	}

	setCmd.Flags().StringVar(&purgeGrace, "purge-grace", "",
		"how long un-demanded data survives before purge, e.g. 24h")

	cmd.AddCommand(getCmd, setCmd)

	return cmd
}
