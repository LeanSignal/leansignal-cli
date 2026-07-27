package cli

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/leansignal/lean-cli/internal/client"
	"github.com/leansignal/lean-cli/internal/output"
)

const dashboardsPath = "/dashboards"

func newDashboardCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "dashboard",
		Aliases: []string{"dashboards", "dash"},
		Short:   "Manage dashboards",
		Long: `Dashboards are Perses documents attached to a demand.

Saving one declares demand: lean-api extracts the metric, log, and trace
selectors from the panel queries and adds them to the demand set, which is what
starts the data flowing centrally.

The document itself is a Perses DashboardSpec. 'get' emits it and 'apply' sends
it back, so a dashboard can live in git between the two.`,
	}

	cmd.AddCommand(
		newDashboardListCommand(f),
		newDashboardGetCommand(f),
		newDashboardApplyCommand(f),
		newDashboardDeleteCommand(f),
		newDashboardVersionsCommand(f),
	)

	return cmd
}

func newDashboardListCommand(f *Factory) *cobra.Command {
	var (
		opts     listFlags
		demandID string
	)

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List dashboards",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o := opts.options()
			if demandID != "" {
				o.Extra = queryOf("demand_id", demandID)
			}

			return runList(cmd, f, dashboardsPath, o,
				func(items []client.Dashboard, _ bool) *output.Table {
					t := output.NewTable("ID", "NAME", "DEMAND", "VERSION", "UPDATED")
					t.Empty = "No dashboards."

					for _, d := range items {
						t.Add(d.ID, d.Name, d.DemandID, d.Version, d.UpdatedAt)
					}

					return t
				})
		},
	}

	opts.bind(cmd)
	cmd.Flags().StringVar(&demandID, "demand", "", "filter by demand id")

	return cmd
}

func newDashboardGetCommand(f *Factory) *cobra.Command {
	var (
		version  int
		specOnly bool
	)

	cmd := &cobra.Command{
		Use:   "get <name|id>",
		Short: "Show a dashboard document",
		Long: `Fetch a dashboard.

--spec emits just the Perses DashboardSpec, which is the shape 'apply' and the
demand bundle expect.`,
		Example: `  leanctl dashboard get "Node overview" --spec > node.json
  leanctl dashboard get <id> --version 3 -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, dashboardsPath, args[0])
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			path := "/dashboard_versions/" + id
			if version > 0 {
				path += "/" + strconv.Itoa(version)
			}

			raw, err := api.Get(ctx, path, nil)
			if err != nil {
				return err
			}

			if specOnly {
				var doc struct {
					Data json.RawMessage `json:"data"`
				}

				if err := jsonUnmarshal(raw, &doc); err != nil {
					return err
				}

				if len(doc.Data) == 0 {
					return fmt.Errorf("dashboard %s has no spec data", args[0])
				}

				_, err := cmd.OutOrStdout().Write(append(doc.Data, '\n'))

				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			var dash client.Dashboard
			if err := jsonUnmarshal(raw, &dash); err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				t := output.NewTable("FIELD", "VALUE")
				t.Add("id", dash.ID)
				t.Add("name", dash.Name)
				t.Add("description", dash.Description)
				t.Add("demand_id", dash.DemandID)
				t.Add("version", dash.Version)
				t.Add("updated_at", dash.UpdatedAt.Format("2006-01-02 15:04:05 MST"))
				t.Add("", "")
				t.Add("spec", "use --spec or -o json to see the panel definitions")

				return t, nil
			})
		},
	}

	cmd.Flags().IntVar(&version, "version", 0, "fetch a specific version (default: latest)")
	cmd.Flags().BoolVar(&specOnly, "spec", false, "emit only the Perses DashboardSpec")

	return cmd
}

func newDashboardApplyCommand(f *Factory) *cobra.Command {
	var (
		file     string
		name     string
		demandID string
	)

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Create or update a dashboard from a file",
		Long: `Send a dashboard document to the server.

Without --id the dashboard is created; with it, a new version of that dashboard
is written. Saving changes the demand set, so panels querying a metric that is
not yet collected will start collecting it.`,
		Example: `  leanctl dashboard apply -f node.json --name "Node overview" --demand <demand-id>
  leanctl dashboard apply -f node.json --id <dashboard-id>`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			body, err := readBody(file)
			if err != nil {
				return err
			}

			overrides := map[string]any{}
			if name != "" {
				overrides["name"] = name
			}

			if demandID != "" {
				overrides["demand_id"] = demandID
			}

			payload, err := mergeBody(body, overrides)
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			id, _ := cmd.Flags().GetString("id")

			var raw []byte
			if id != "" {
				raw, err = api.Put(ctx, "/dashboard/"+id, nil, payload)
			} else {
				raw, err = api.Post(ctx, "/dashboard", nil, payload)
			}

			if err != nil {
				return err
			}

			var saved client.Dashboard
			if err := jsonUnmarshal(raw, &saved); err != nil {
				return err
			}

			return emitAction(f, raw, "Applied dashboard %q (%s), version %d",
				saved.Name, saved.ID, saved.Version)
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "dashboard JSON ('-' for stdin)")
	cmd.Flags().StringVar(&name, "name", "", "dashboard name (overrides the file)")
	cmd.Flags().StringVar(&demandID, "demand", "", "demand id to attach to (overrides the file)")
	cmd.Flags().String("id", "", "update this dashboard instead of creating one")

	if err := cmd.MarkFlagRequired("file"); err != nil {
		panic(err)
	}

	return cmd
}

func newDashboardDeleteCommand(f *Factory) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     "delete <name|id>",
		Aliases: []string{"rm"},
		Short:   "Delete a dashboard",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, dashboardsPath, args[0])
			if err != nil {
				return err
			}

			if err := confirm(cmd, yes, "delete dashboard %q", args[0]); err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			if err := api.Delete(ctx, "/dashboard/"+id, nil); err != nil {
				return err
			}

			return emitAction(f, nil, "Deleted dashboard %s", args[0])
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

func newDashboardVersionsCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "versions <name|id>",
		Short: "Show the current version of a dashboard",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveID(cmd, f, dashboardsPath, args[0])
			if err != nil {
				return err
			}

			return runGet(cmd, f, "/dashboard_versions/"+id, nil,
				func(d client.Dashboard, _ bool) *output.Table {
					t := output.NewTable("NAME", "VERSION", "UPDATED")
					t.Add(d.Name, d.Version, d.UpdatedAt)

					return t
				})
		},
	}

	return cmd
}
