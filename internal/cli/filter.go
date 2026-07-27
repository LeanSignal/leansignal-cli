package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/leansignal/lean-cli/internal/client"
	"github.com/leansignal/lean-cli/internal/output"
)

func newFilterCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "filter",
		Aliases: []string{"filters", "demand-set"},
		Short:   "Inspect the demand set",
		Long: `The demand set is the materialized answer to "what does this tenant store?".

Every dashboard panel, active alert rule, and custom rule contributes selectors;
lean-api merges them into the filters table and pushes the result down to the
agents. Nothing outside this set is stored centrally.`,
	}

	cmd.AddCommand(
		newFilterListCommand(f),
		newFilterPurgedCommand(f),
		newFilterSyncCommand(f),
		newFilterSweepCommand(f),
	)

	return cmd
}

func newFilterListCommand(f *Factory) *cobra.Command {
	var (
		opts       listFlags
		filterType string
		status     string
	)

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List demand-set filters",
		Example: `  leanctl filter list --type log
  leanctl filter list --type metric --all -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o := opts.options()
			o.Extra = queryOf("type", filterType, "status", status)

			return runList(cmd, f, "/filters", o,
				func(items []client.Filter, wide bool) *output.Table {
					headers := []string{"TYPE", "RULE", "STATUS", "SOURCE", "UPDATED"}
					if wide {
						headers = append([]string{"ID"}, append(headers, "DEMAND")...)
					}

					t := output.NewTable(headers...)
					t.Empty = "The demand set is empty — nothing is being stored centrally."

					for _, fl := range items {
						rule := output.Truncate(fl.Rule, 64)
						if wide {
							t.Add(fl.ID, fl.Type, rule, fl.Status, fl.ObjectType, fl.UpdatedAt, fl.DemandID)

							continue
						}

						t.Add(fl.Type, rule, fl.Status, fl.ObjectType, fl.UpdatedAt)
					}

					return t
				})
		},
	}

	opts.bind(cmd)
	cmd.Flags().StringVar(&filterType, "type", "", "filter by signal: metric, log, trace")
	cmd.Flags().StringVar(&status, "status", "", "filter by status")

	return cmd
}

func newFilterPurgedCommand(f *Factory) *cobra.Command {
	var opts listFlags

	cmd := &cobra.Command{
		Use:   "purged",
		Short: "Show what the purge worker removed",
		Long: `List filters that were deleted and the data removed with them.

When a filter goes away, whatever it was the only demand for stops being
demanded; after the tenant's grace window the purge worker deletes it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd, f, "/filters/purged", opts.options(),
				func(items []client.PurgedFilter, _ bool) *output.Table {
					t := output.NewTable("TYPE", "RULE", "DEMAND", "OUTCOME", "PURGED", "PROCESSED")
					t.Empty = "Nothing has been purged."

					for _, p := range items {
						t.Add(p.Type, output.Truncate(p.Rule, 56), p.DemandName, p.Outcome,
							p.PurgedCount, p.ProcessedAt)
					}

					return t
				})
		},
	}

	opts.bind(cmd)

	return cmd
}

func newFilterSyncCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Re-materialize the demand set and push it to agents",
		Long: `Force a full re-scan of dashboards, alert rules, and custom rules.

This normally happens on its own — on every mutation and on a 15-second
checkpoint scan — so reach for it only when an agent's demand set looks stale.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			api, err := f.Client()
			if err != nil {
				return err
			}

			raw, err := api.Post(ctx, "/filters/sync", nil, nil)
			if err != nil {
				return err
			}

			return emitAction(f, raw, "Demand set re-synced and broadcast to connected agents.")
		},
	}

	return cmd
}

func newFilterSweepCommand(f *Factory) *cobra.Command {
	var (
		dryRun bool
		status bool
		yes    bool
	)

	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Collect garbage: delete stored data nothing demands",
		Long: `Run a mark-and-sweep over the central stores.

Everything any live filter matches is kept — including data that has been silent
for weeks, which is legitimate history and ages out only via retention. What no
filter matches is deleted.

Use --dry-run first: it reports exactly what would go. If part of a store could
not be enumerated the report says so, and a candidate count of zero must not be
read as "clean" in that case.`,
		Example: `  leanctl filter sweep --dry-run
  leanctl filter sweep --status`,
		Args: cobra.NoArgs,
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

			if status {
				raw, err := api.Get(ctx, "/filters/sweep", nil)
				if err != nil {
					return err
				}

				return p.Emit(raw, func() (*output.Table, error) { return sweepTable(raw) })
			}

			if !dryRun {
				if err := confirm(cmd, yes,
					"delete every stored series, stream, and span that no filter demands"); err != nil {
					return err
				}
			}

			q := queryOf()
			if dryRun {
				q.Set("dry_run", "true")
			}

			raw, err := api.Post(ctx, "/filters/sweep", q, nil)
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) { return sweepTable(raw) })
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be deleted, delete nothing")
	cmd.Flags().BoolVar(&status, "status", false, "show the progress of the running or last sweep")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

func sweepTable(raw []byte) (*output.Table, error) {
	var run struct {
		State   string `json:"state"`
		DryRun  bool   `json:"dry_run"`
		Metrics struct {
			Enumerated   bool     `json:"enumerated"`
			Candidates   int      `json:"candidates"`
			Deleted      int      `json:"deleted"`
			SkippedNames []string `json:"skipped_names"`
		} `json:"metrics"`
		Logs struct {
			Enumerated bool `json:"enumerated"`
			Candidates int  `json:"candidates"`
			Deleted    int  `json:"deleted"`
		} `json:"logs"`
		Traces struct {
			OrgsDropped int `json:"orgs_dropped"`
		} `json:"traces"`
		Error string `json:"error"`
	}

	if err := jsonUnmarshal(raw, &run); err != nil {
		return nil, err
	}

	t := output.NewTable("STORE", "CANDIDATES", "DELETED", "COMPLETE")
	t.Add("metrics", run.Metrics.Candidates, run.Metrics.Deleted, run.Metrics.Enumerated)
	t.Add("logs", run.Logs.Candidates, run.Logs.Deleted, run.Logs.Enumerated)
	t.Add("traces (orgs)", "-", run.Traces.OrgsDropped, true)

	if len(run.Metrics.SkippedNames) > 0 {
		t.Add("", "", "", fmt.Sprintf("%d metric name(s) could not be enumerated",
			len(run.Metrics.SkippedNames)))
	}

	if run.Error != "" {
		t.Add("error", run.Error, "", "")
	}

	return t, nil
}
