package cli

import (
	"github.com/spf13/cobra"

	"github.com/leansignal/leansignal-cli/internal/client"
	"github.com/leansignal/leansignal-cli/internal/output"
)

const syntheticsPath = "/synthetics"

func newSyntheticCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "synthetic",
		Aliases: []string{"synthetics", "check"},
		Short:   "Manage synthetic checks",
		Long: `A synthetic check probes an HTTP endpoint on a schedule and alerts on the
up/down transition, through the same notification channels as alert rules.`,
	}

	cmd.AddCommand(
		newSyntheticListCommand(f),
		newSyntheticGetCommand(f),
		newSyntheticCreateCommand(f),
		newSyntheticUpdateCommand(f),
		newSyntheticDeleteCommand(f),
		newSyntheticStateCommand(f, "pause", "Pause a check"),
		newSyntheticStateCommand(f, "resume", "Resume a paused check"),
		newSyntheticTestCommand(f),
		newSyntheticResultsCommand(f),
	)

	return cmd
}

func newSyntheticListCommand(f *Factory) *cobra.Command {
	var opts listFlags

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List synthetic checks",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd, f, syntheticsPath, opts.options(),
				func(items []client.SyntheticCheck, wide bool) *output.Table {
					headers := []string{"NAME", "METHOD", "URL", "STATUS", "INTERVAL", "LAST RUN"}
					if wide {
						headers = append([]string{"ID"}, append(headers, "LATENCY", "CODE")...)
					}

					t := output.NewTable(headers...)
					t.Empty = "No synthetic checks."

					for _, s := range items {
						status := s.Status
						if s.Paused {
							status = "paused"
						}

						if wide {
							t.Add(s.ID, s.Name, s.Method, output.Truncate(s.URL, 48), status,
								s.Interval, s.LastRunAt, s.LastLatencyMS, s.LastStatus)

							continue
						}

						t.Add(s.Name, s.Method, output.Truncate(s.URL, 48), status, s.Interval, s.LastRunAt)
					}

					return t
				})
		},
	}

	opts.bind(cmd)

	return cmd
}

func newSyntheticGetCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <name|id>",
		Short: "Show one synthetic check",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveID(cmd, f, syntheticsPath, args[0])
			if err != nil {
				return err
			}

			return runGet(cmd, f, "/synthetic/"+id, nil,
				func(s client.SyntheticCheck, _ bool) *output.Table {
					t := output.NewTable("FIELD", "VALUE")
					t.Add("id", s.ID)
					t.Add("name", s.Name)
					t.Add("target", s.Method+" "+s.URL)
					t.Add("status", s.Status)
					t.Add("paused", s.Paused)
					t.Add("interval", s.Interval)
					t.Add("last_run_at", s.LastRunAt)
					t.Add("last_result_ok", s.LastResultOK)
					t.Add("last_status_code", s.LastStatus)
					t.Add("last_latency_ms", s.LastLatencyMS)
					t.Add("last_error", s.LastError)

					return t
				})
		},
	}

	return cmd
}

type syntheticBodyFlags struct {
	file     string
	name     string
	url      string
	method   string
	interval string
	demandID string
	expected int
	channels []string
	severity string
}

func (s *syntheticBodyFlags) bind(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringVarP(&s.file, "file", "f", "", "JSON body ('-' for stdin)")
	fs.StringVar(&s.name, "name", "", "check name")
	fs.StringVar(&s.url, "url", "", "URL to probe")
	fs.StringVar(&s.method, "method", "", "HTTP method (default GET)")
	fs.StringVar(&s.interval, "interval", "", "probe interval, e.g. 1m, 5m")
	fs.StringVar(&s.demandID, "demand", "", "demand id this check belongs to")
	fs.IntVar(&s.expected, "expect-status", 0, "expected HTTP status code")
	fs.StringArrayVar(&s.channels, "channel", nil, "notification channel id (repeatable)")
	fs.StringVar(&s.severity, "severity", "", "severity: info, warning, critical")
}

func (s *syntheticBodyFlags) body(cmd *cobra.Command) ([]byte, error) {
	var base []byte

	if s.file != "" {
		var err error
		if base, err = readBody(s.file); err != nil {
			return nil, err
		}
	}

	overrides := map[string]any{}
	putIf(overrides, "name", s.name)
	putIf(overrides, "url", s.url)
	putIf(overrides, "method", s.method)
	putIf(overrides, "interval", s.interval)
	putIf(overrides, "demand_id", s.demandID)
	putIf(overrides, "severity", s.severity)

	if cmd.Flags().Changed("expect-status") {
		overrides["expected_status"] = s.expected
	}

	if len(s.channels) > 0 {
		overrides["channel_ids"] = s.channels
	}

	return mergeBody(base, overrides)
}

func newSyntheticCreateCommand(f *Factory) *cobra.Command {
	flags := &syntheticBodyFlags{}

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a synthetic check",
		Example: `  leanctl synthetic create --name api-health --url https://api.example.com/healthz \
    --interval 1m --expect-status 200 --demand <id> --channel <channel-id>`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			body, err := flags.body(cmd)
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			raw, err := api.Post(ctx, "/synthetic", nil, body)
			if err != nil {
				return err
			}

			var created client.SyntheticCheck
			if err := jsonUnmarshal(raw, &created); err != nil {
				return err
			}

			return emitAction(f, raw, "Created synthetic check %q (%s)", created.Name, created.ID)
		},
	}

	flags.bind(cmd)

	return cmd
}

func newSyntheticUpdateCommand(f *Factory) *cobra.Command {
	flags := &syntheticBodyFlags{}

	cmd := &cobra.Command{
		Use:   "update <name|id>",
		Short: "Update a synthetic check",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, syntheticsPath, args[0])
			if err != nil {
				return err
			}

			body, err := flags.body(cmd)
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			raw, err := api.Put(ctx, "/synthetic/"+id, nil, body)
			if err != nil {
				return err
			}

			return emitAction(f, raw, "Updated synthetic check %s", args[0])
		},
	}

	flags.bind(cmd)

	return cmd
}

func newSyntheticDeleteCommand(f *Factory) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     "delete <name|id>",
		Aliases: []string{"rm"},
		Short:   "Delete a synthetic check",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, syntheticsPath, args[0])
			if err != nil {
				return err
			}

			if err := confirm(cmd, yes, "delete synthetic check %q", args[0]); err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			if err := api.Delete(ctx, "/synthetic/"+id, nil); err != nil {
				return err
			}

			return emitAction(f, nil, "Deleted synthetic check %s", args[0])
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

func newSyntheticStateCommand(f *Factory, action, short string) *cobra.Command {
	return &cobra.Command{
		Use:   action + " <name|id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, syntheticsPath, args[0])
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			raw, err := api.Post(ctx, "/synthetic/"+id+"/"+action, nil, nil)
			if err != nil {
				return err
			}

			return emitAction(f, raw, "%sd synthetic check %s", capitalize(action), args[0])
		},
	}
}

func newSyntheticTestCommand(f *Factory) *cobra.Command {
	var file string

	cmd := &cobra.Command{
		Use:   "test [name|id]",
		Short: "Run a check once, right now",
		Long: `Probe immediately and report the result.

With an argument the saved check is run; with --file an unsaved definition is
probed, which is the way to try a check before creating it.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			api, err := f.Client()
			if err != nil {
				return err
			}

			var (
				raw  []byte
				path = "/synthetic/test"
				body []byte
			)

			switch {
			case len(args) == 1:
				id, idErr := resolveID(cmd, f, syntheticsPath, args[0])
				if idErr != nil {
					return idErr
				}

				path = "/synthetic/" + id + "/test"
			case file != "":
				if body, err = readBody(file); err != nil {
					return err
				}
			default:
				return client.Usage("pass a check name/id, or --file with a definition to try")
			}

			if raw, err = api.Post(ctx, path, nil, body); err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				var res struct {
					OK         bool   `json:"ok"`
					StatusCode int    `json:"status_code"`
					LatencyMS  int    `json:"latency_ms"`
					Error      string `json:"error"`
				}

				if err := jsonUnmarshal(raw, &res); err != nil {
					return nil, err
				}

				t := output.NewTable("OK", "STATUS", "LATENCY(ms)", "ERROR")
				t.Add(res.OK, res.StatusCode, res.LatencyMS, res.Error)

				return t, nil
			})
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "probe an unsaved definition from a JSON file")

	return cmd
}

func newSyntheticResultsCommand(f *Factory) *cobra.Command {
	var opts listFlags

	cmd := &cobra.Command{
		Use:   "results <name|id>",
		Short: "Show recent probe results",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveID(cmd, f, syntheticsPath, args[0])
			if err != nil {
				return err
			}

			type result struct {
				RunAt      string `json:"run_at"`
				OK         bool   `json:"ok"`
				StatusCode int    `json:"status_code"`
				LatencyMS  int    `json:"latency_ms"`
				Error      string `json:"error"`
			}

			return runList(cmd, f, "/synthetic/"+id+"/results", opts.options(),
				func(items []result, _ bool) *output.Table {
					t := output.NewTable("RUN AT", "OK", "STATUS", "LATENCY(ms)", "ERROR")
					t.Empty = "No probe results yet."

					for _, r := range items {
						t.Add(r.RunAt, r.OK, r.StatusCode, r.LatencyMS, output.Truncate(r.Error, 48))
					}

					return t
				})
		},
	}

	opts.bind(cmd)

	return cmd
}
