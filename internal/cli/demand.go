package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/leansignal/lean-cli/internal/client"
	"github.com/leansignal/lean-cli/internal/output"
)

const demandsPath = "/demands"

func newDemandCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "demand",
		Aliases: []string{"demands"},
		Short:   "Manage demands",
		Long: `A demand is what makes telemetry worth storing.

Nothing undemanded is kept centrally, so a demand — and the dashboards, alert
rules, and custom rules under it — is the unit that decides what LeanSignal
stores. Export and import round-trip a whole demand, which is what makes demands
reviewable in git.`,
	}

	cmd.AddCommand(
		newDemandListCommand(f),
		newDemandGetCommand(f),
		newDemandCreateCommand(f),
		newDemandUpdateCommand(f),
		newDemandDeleteCommand(f),
		newDemandExportCommand(f),
		newDemandImportCommand(f),
	)

	return cmd
}

func demandTable(items []client.Demand, wide bool) *output.Table {
	headers := []string{"ID", "NAME", "DESCRIPTION", "CREATED BY", "AGE"}
	if !wide {
		headers = []string{"NAME", "DESCRIPTION", "CREATED BY", "AGE"}
	}

	t := output.NewTable(headers...)
	t.Empty = "No demands. Create one with 'leanctl demand create --name <name>'."

	for _, d := range items {
		desc := output.Truncate(d.Description, 48)
		if wide {
			t.Add(d.ID, d.Name, desc, d.CreatedByEmail, d.CreatedAt)
		} else {
			t.Add(d.Name, desc, d.CreatedByEmail, d.CreatedAt)
		}
	}

	if !wide {
		t.IDColumn = 0
	}

	return t
}

func newDemandListCommand(f *Factory) *cobra.Command {
	var opts listFlags

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List demands",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd, f, demandsPath, opts.options(), demandTable)
		},
	}

	opts.bind(cmd)

	return cmd
}

func newDemandGetCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <name|id>",
		Short: "Show one demand",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveID(cmd, f, demandsPath, args[0])
			if err != nil {
				return err
			}

			return runGet(cmd, f, "/demand/"+id, nil, func(d client.Demand, _ bool) *output.Table {
				t := output.NewTable("FIELD", "VALUE")
				t.Add("id", d.ID)
				t.Add("name", d.Name)
				t.Add("description", d.Description)
				t.Add("created_by", d.CreatedByEmail)
				t.Add("created_at", d.CreatedAt.Format("2006-01-02 15:04:05 MST"))
				t.Add("updated_at", d.UpdatedAt.Format("2006-01-02 15:04:05 MST"))

				return t
			})
		},
	}

	return cmd
}

func newDemandCreateCommand(f *Factory) *cobra.Command {
	var (
		name        string
		description string
		file        string
	)

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "Create a demand",
		Example: `  leanctl demand create --name kubernetes --description "cluster health"`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			var base []byte

			if file != "" {
				var err error
				if base, err = readBody(file); err != nil {
					return err
				}
			}

			overrides := map[string]any{}
			if name != "" {
				overrides["name"] = name
			}

			if description != "" {
				overrides["description"] = description
			}

			body, err := mergeBody(base, overrides)
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			raw, err := api.Post(ctx, "/demand", nil, body)
			if err != nil {
				return err
			}

			var created client.Demand
			if err := jsonUnmarshal(raw, &created); err != nil {
				return err
			}

			return emitAction(f, raw, "Created demand %q (%s)", created.Name, created.ID)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "demand name")
	cmd.Flags().StringVar(&description, "description", "", "demand description")
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON body ('-' for stdin)")

	return cmd
}

func newDemandUpdateCommand(f *Factory) *cobra.Command {
	var (
		name        string
		description string
		file        string
	)

	cmd := &cobra.Command{
		Use:     "update <name|id>",
		Short:   "Update a demand",
		Example: `  leanctl demand get kubernetes -o json > d.json && leanctl demand update kubernetes -f d.json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, demandsPath, args[0])
			if err != nil {
				return err
			}

			var base []byte

			if file != "" {
				if base, err = readBody(file); err != nil {
					return err
				}
			}

			overrides := map[string]any{}
			if name != "" {
				overrides["name"] = name
			}

			if description != "" {
				overrides["description"] = description
			}

			body, err := mergeBody(base, overrides)
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			raw, err := api.Put(ctx, "/demand/"+id, nil, body)
			if err != nil {
				return err
			}

			return emitAction(f, raw, "Updated demand %s", args[0])
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "new demand name")
	cmd.Flags().StringVar(&description, "description", "", "new description")
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON body ('-' for stdin)")

	return cmd
}

func newDemandDeleteCommand(f *Factory) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     "delete <name|id>",
		Aliases: []string{"rm"},
		Short:   "Delete a demand and everything under it",
		Long: `Delete a demand.

Its dashboards and alert rules cascade with it. Whatever the demand was the only
reason to store stops being demanded, and the purge worker removes that data
after the tenant's grace window.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, demandsPath, args[0])
			if err != nil {
				return err
			}

			if err := confirm(cmd, yes,
				"delete demand %q and its dashboards and alert rules", args[0]); err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			if err := api.Delete(ctx, "/demand/"+id, nil); err != nil {
				return err
			}

			return emitAction(f, nil, "Deleted demand %s", args[0])
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

func newDemandExportCommand(f *Factory) *cobra.Command {
	var outFile string

	cmd := &cobra.Command{
		Use:   "export <name|id>",
		Short: "Export a demand bundle (dashboards + alert rules)",
		Long: `Write a portable JSON bundle of a demand and everything under it.

Server-owned fields are stripped and notification channels are referenced by
name, so the bundle can be imported into any tenant. Commit it to git and the
demand becomes reviewable.`,
		Example: `  leanctl demand export host-metrics > demands/host-metrics.json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, demandsPath, args[0])
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			raw, err := api.Get(ctx, "/demand/"+id+"/export", nil)
			if err != nil {
				return err
			}

			if outFile == "" {
				_, err := cmd.OutOrStdout().Write(append(raw, '\n'))

				return err
			}

			if err := os.WriteFile(outFile, raw, 0o600); err != nil {
				return fmt.Errorf("writing %s: %w", outFile, err)
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %s (%d bytes)\n", outFile, len(raw))

			return nil
		},
	}

	cmd.Flags().StringVarP(&outFile, "out", "O", "", "write to a file instead of stdout")

	return cmd
}

func newDemandImportCommand(f *Factory) *cobra.Command {
	var (
		file     string
		fromName string
		dryRun   bool
	)

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import a demand bundle",
		Long: `Recreate a demand and its dashboards and alert rules from a bundle.

Notification channels are matched by name against this tenant: a rule whose
channels are all missing is imported paused, and the misses are reported rather
than treated as an error. Use --dry-run in CI to validate a bundle without
writing anything.`,
		Example: `  leanctl demand import --file demands/host-metrics.json --dry-run
  leanctl demand import --name host-metrics-otel-demand-standard`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			if (file == "") == (fromName == "") {
				return client.Usage("pass exactly one of --file or --name")
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			q := queryOf()
			if dryRun {
				q.Set("dry_run", "true")
			}

			var raw []byte

			if file != "" {
				body, err := readBody(file)
				if err != nil {
					return err
				}

				raw, err = api.Post(ctx, "/demand/import", q, body)
				if err != nil {
					return err
				}
			} else {
				body := mustJSON(map[string]any{"demand_public_name": fromName})

				raw, err = api.Post(ctx, "/demand/import_by_name", q, body)
				if err != nil {
					return err
				}
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			if p.Structured() {
				return p.Emit(raw, nil)
			}

			return printImportReport(cmd, raw, dryRun)
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "bundle file ('-' for stdin)")
	cmd.Flags().StringVar(&fromName, "name", "", "public demand name from the LeanSignal registry")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate without writing anything")

	return cmd
}

// printImportReport renders the import result, keeping the warnings visible —
// a partially-linked import that looks like a clean success is the failure mode
// worth guarding against.
func printImportReport(cmd *cobra.Command, raw []byte, dryRun bool) error {
	var report struct {
		DemandID        string   `json:"demand_id"`
		DemandName      string   `json:"demand_name"`
		Dashboards      int      `json:"dashboards_created"`
		AlertRules      int      `json:"alert_rules_created"`
		ChannelsMissing []string `json:"channels_missing"`
		Warnings        []string `json:"warnings"`
	}

	if err := jsonUnmarshal(raw, &report); err != nil {
		_, writeErr := cmd.OutOrStdout().Write(append(raw, '\n'))

		return writeErr
	}

	verb := "Imported"
	if dryRun {
		verb = "Would import"
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s demand %q: %d dashboard(s), %d alert rule(s)\n",
		verb, report.DemandName, report.Dashboards, report.AlertRules)

	if len(report.ChannelsMissing) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "Missing notification channels (rules left unlinked and paused): %v\n",
			report.ChannelsMissing)
	}

	for _, w := range report.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", w)
	}

	return nil
}
