package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/leansignal/leansignal-cli/internal/client"
	"github.com/leansignal/leansignal-cli/internal/output"
)

const alertRulesPath = "/alert_rules"

func newAlertCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "alert",
		Aliases: []string{"alerts"},
		Short:   "Manage alert rules",
		Long: `An alert rule is a PromQL query plus a condition, attached to a demand and
notifying a set of channels.

Creating a rule also declares demand: the metrics it queries start being stored
centrally, exactly as a dashboard would. Evaluation is minute-granular, and a
rule with no enabled channel is kept paused because it has nowhere to deliver.`,
	}

	cmd.AddCommand(
		newAlertListCommand(f),
		newAlertGetCommand(f),
		newAlertCreateCommand(f),
		newAlertUpdateCommand(f),
		newAlertDeleteCommand(f),
		newAlertStateCommand(f, "pause", "Pause a rule (stops evaluation)"),
		newAlertStateCommand(f, "resume", "Resume a paused rule"),
		newAlertStateCommand(f, "mute", "Mute notifications while still evaluating"),
		newAlertStateCommand(f, "unmute", "Unmute notifications"),
		newAlertTestCommand(f),
	)

	return cmd
}

func alertTable(items []client.AlertRule, wide bool) *output.Table {
	headers := []string{"NAME", "STATUS", "SEVERITY", "CONDITION", "INTERVAL", "LAST FIRED"}
	if wide {
		headers = append([]string{"ID"}, append(headers, "QUERY")...)
	}

	t := output.NewTable(headers...)
	t.Empty = "No alert rules."

	for _, a := range items {
		status := a.Status
		if a.Paused {
			status = "paused"
		}

		cond := a.ConditionOp + " " + output.Cell(a.Threshold)

		if wide {
			t.Add(a.ID, a.Name, status, a.Severity, cond, a.Interval, a.LastFiredAt,
				output.Truncate(a.Query, 60))

			continue
		}

		t.Add(a.Name, status, a.Severity, cond, a.Interval, a.LastFiredAt)
	}

	return t
}

func newAlertListCommand(f *Factory) *cobra.Command {
	var (
		opts     listFlags
		demandID string
	)

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List alert rules",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o := opts.options()
			if demandID != "" {
				o.Extra = queryOf("demand_id", demandID)
			}

			return runList(cmd, f, alertRulesPath, o, alertTable)
		},
	}

	opts.bind(cmd)
	cmd.Flags().StringVar(&demandID, "demand", "", "filter by demand id")

	return cmd
}

func newAlertGetCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <name|id>",
		Short: "Show one alert rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveID(cmd, f, alertRulesPath, args[0])
			if err != nil {
				return err
			}

			return runGet(cmd, f, "/alert_rule/"+id, nil, func(a client.AlertRule, _ bool) *output.Table {
				t := output.NewTable("FIELD", "VALUE")
				t.Add("id", a.ID)
				t.Add("name", a.Name)
				t.Add("query", a.Query)
				t.Add("condition", a.ConditionOp+" "+output.Cell(a.Threshold))
				t.Add("severity", a.Severity)
				t.Add("status", a.Status)
				t.Add("paused", a.Paused)
				t.Add("interval", a.Interval)
				t.Add("for", a.For)
				t.Add("channels", a.ChannelIDs)
				t.Add("muted_until", a.MutedUntil)
				t.Add("last_eval_at", a.LastEvalAt)
				t.Add("last_fired_at", a.LastFiredAt)
				t.Add("demand_id", a.DemandID)

				return t
			})
		},
	}

	return cmd
}

// alertBodyFlags are the fields shared by create and update. Complex rules are
// easier to manage as a file, so --file is always available too.
type alertBodyFlags struct {
	file        string
	name        string
	query       string
	conditionOp string
	threshold   float64
	severity    string
	interval    string
	forDuration string
	demandID    string
	channels    []string
	message     string
}

func (a *alertBodyFlags) bind(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringVarP(&a.file, "file", "f", "", "JSON body ('-' for stdin)")
	fs.StringVar(&a.name, "name", "", "rule name")
	fs.StringVar(&a.query, "query", "", "PromQL query")
	fs.StringVar(&a.conditionOp, "condition", "", "condition operator: gt, gte, lt, lte, eq, neq")
	fs.Float64Var(&a.threshold, "threshold", 0, "threshold the condition compares against")
	fs.StringVar(&a.severity, "severity", "", "severity: info, warning, critical")
	fs.StringVar(&a.interval, "interval", "", "evaluation interval (minutes or coarser, e.g. 1m, 5m)")
	fs.StringVar(&a.forDuration, "for", "", "how long the breach must hold before firing, e.g. 5m")
	fs.StringVar(&a.demandID, "demand", "", "demand id this rule belongs to")
	fs.StringArrayVar(&a.channels, "channel", nil, "notification channel id (repeatable)")
	fs.StringVar(&a.message, "message", "", "notification message template")
}

func (a *alertBodyFlags) body(cmd *cobra.Command) ([]byte, error) {
	var base []byte

	if a.file != "" {
		var err error
		if base, err = readBody(a.file); err != nil {
			return nil, err
		}
	}

	overrides := map[string]any{}
	putIf(overrides, "name", a.name)
	putIf(overrides, "query", a.query)
	putIf(overrides, "condition_op", a.conditionOp)
	putIf(overrides, "severity", a.severity)
	putIf(overrides, "interval", a.interval)
	putIf(overrides, "for", a.forDuration)
	putIf(overrides, "demand_id", a.demandID)
	putIf(overrides, "message", a.message)

	if cmd.Flags().Changed("threshold") {
		overrides["threshold"] = a.threshold
	}

	if len(a.channels) > 0 {
		overrides["channel_ids"] = a.channels
	}

	return mergeBody(base, overrides)
}

func newAlertCreateCommand(f *Factory) *cobra.Command {
	flags := &alertBodyFlags{}

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an alert rule",
		Example: `  leanctl alert create --name "High CPU" --demand <id> \
    --query 'avg(rate(cpu_seconds_total[5m]))' --condition gt --threshold 0.9 \
    --severity warning --interval 1m --for 5m --channel <channel-id>`,
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

			raw, err := api.Post(ctx, "/alert_rule", nil, body)
			if err != nil {
				return err
			}

			var created client.AlertRule
			if err := jsonUnmarshal(raw, &created); err != nil {
				return err
			}

			if err := emitAction(f, raw, "Created alert rule %q (%s)", created.Name, created.ID); err != nil {
				return err
			}

			if created.Paused && len(created.ChannelIDs) == 0 {
				p, perr := f.Printer()
				if perr == nil {
					p.Note("Rule has no notification channel, so it was created paused. " +
						"Attach one with --channel and run 'leanctl alert resume'.")
				}
			}

			return nil
		},
	}

	flags.bind(cmd)

	return cmd
}

func newAlertUpdateCommand(f *Factory) *cobra.Command {
	flags := &alertBodyFlags{}

	cmd := &cobra.Command{
		Use:   "update <name|id>",
		Short: "Update an alert rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, alertRulesPath, args[0])
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

			raw, err := api.Put(ctx, "/alert_rule/"+id, nil, body)
			if err != nil {
				return err
			}

			return emitAction(f, raw, "Updated alert rule %s", args[0])
		},
	}

	flags.bind(cmd)

	return cmd
}

func newAlertDeleteCommand(f *Factory) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     "delete <name|id>",
		Aliases: []string{"rm"},
		Short:   "Delete an alert rule",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, alertRulesPath, args[0])
			if err != nil {
				return err
			}

			if err := confirm(cmd, yes, "delete alert rule %q", args[0]); err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			if err := api.Delete(ctx, "/alert_rule/"+id, nil); err != nil {
				return err
			}

			return emitAction(f, nil, "Deleted alert rule %s", args[0])
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

// newAlertStateCommand builds the pause/resume/mute/unmute quartet, which all
// POST to /alert_rule/{id}/<action>.
func newAlertStateCommand(f *Factory, action, short string) *cobra.Command {
	var until string

	cmd := &cobra.Command{
		Use:   action + " <name|id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, alertRulesPath, args[0])
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			var body []byte
			if action == "mute" && until != "" {
				body = mustJSON(map[string]any{"muted_until": until})
			}

			raw, err := api.Post(ctx, "/alert_rule/"+id+"/"+action, nil, body)
			if err != nil {
				return err
			}

			return emitAction(f, raw, "%sd alert rule %s", capitalize(action), args[0])
		},
	}

	if action == "mute" {
		cmd.Flags().StringVar(&until, "until", "", "mute until this RFC3339 timestamp")
	}

	return cmd
}

func newAlertTestCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test <name|id>",
		Short: "Evaluate a rule now without changing state",
		Long: `Run the rule's query against the dataplane and report whether it would breach.

Nothing is persisted and no notification is sent.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, alertRulesPath, args[0])
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			raw, err := api.Post(ctx, "/alert_rule/"+id+"/test", nil, nil)
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				var result struct {
					Breaching bool `json:"breaching"`
					Values    []struct {
						Value  float64           `json:"value"`
						Labels map[string]string `json:"labels"`
					} `json:"values"`
				}

				if err := jsonUnmarshal(raw, &result); err != nil {
					return nil, err
				}

				t := output.NewTable("BREACHING", "VALUE", "LABELS")
				t.Empty = "The query returned no series."

				for _, v := range result.Values {
					t.Add(result.Breaching, v.Value, formatLabels(v.Labels))
				}

				return t, nil
			})
		},
	}

	return cmd
}

func putIf(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

// capitalize upper-cases the first letter, so "pause" becomes the "Paused" in
// a confirmation line. Command verbs are ASCII by construction.
func capitalize(s string) string {
	if s == "" {
		return s
	}

	return strings.ToUpper(s[:1]) + s[1:]
}
