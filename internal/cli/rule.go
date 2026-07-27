package cli

import (
	"github.com/spf13/cobra"

	"github.com/leansignal/lean-cli/internal/client"
	"github.com/leansignal/lean-cli/internal/output"
)

const customRulesPath = "/custom_rules"

func newRuleCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rule",
		Aliases: []string{"rules"},
		Short:   "Manage custom rules (hand-written demand)",
		Long: `A custom rule declares demand directly, without a dashboard or an alert.

Each rule is one expression in its signal's query language — a PromQL selector
for metrics, a LogQL stream selector for logs, a resource selector for traces —
attached to a demand. An enabled rule makes the agent forward what it matches.

This is the CLI equivalent of the "Custom rules" section on the Metrics, Logs,
and Traces pages.`,
	}

	cmd.AddCommand(
		newRuleListCommand(f),
		newRuleGetCommand(f),
		newRuleCreateCommand(f),
		newRuleUpdateCommand(f),
		newRuleDeleteCommand(f),
		newRuleStateCommand(f, "enable", "Enable a rule (starts forwarding)"),
		newRuleStateCommand(f, "disable", "Disable a rule (stops forwarding)"),
	)

	return cmd
}

func newRuleListCommand(f *Factory) *cobra.Command {
	var (
		opts listFlags
		kind string
	)

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List custom rules",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o := opts.options()
			if kind != "" {
				o.Extra = queryOf("kind", kind)
			}

			return runList(cmd, f, customRulesPath, o,
				func(items []client.CustomRule, wide bool) *output.Table {
					headers := []string{"NAME", "KIND", "ENABLED", "EXPRESSION", "UPDATED"}
					if wide {
						headers = append([]string{"ID"}, append(headers, "DEMAND")...)
					}

					t := output.NewTable(headers...)
					t.Empty = "No custom rules."

					for _, r := range items {
						expr := output.Truncate(r.Expression, 56)
						if wide {
							t.Add(r.ID, r.Name, r.Kind, r.Enabled, expr, r.UpdatedAt, r.DemandID)

							continue
						}

						t.Add(r.Name, r.Kind, r.Enabled, expr, r.UpdatedAt)
					}

					return t
				})
		},
	}

	opts.bind(cmd)
	cmd.Flags().StringVar(&kind, "kind", "", "filter by kind: metric, log, trace")

	return cmd
}

func newRuleGetCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <name|id>",
		Short: "Show one custom rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveID(cmd, f, customRulesPath, args[0])
			if err != nil {
				return err
			}

			return runGet(cmd, f, "/custom_rule/"+id, nil, func(r client.CustomRule, _ bool) *output.Table {
				t := output.NewTable("FIELD", "VALUE")
				t.Add("id", r.ID)
				t.Add("name", r.Name)
				t.Add("kind", r.Kind)
				t.Add("expression", r.Expression)
				t.Add("enabled", r.Enabled)
				t.Add("demand_id", r.DemandID)

				return t
			})
		},
	}

	return cmd
}

type ruleBodyFlags struct {
	file       string
	name       string
	kind       string
	expression string
	demandID   string
	enabled    bool
}

func (r *ruleBodyFlags) bind(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringVarP(&r.file, "file", "f", "", "JSON body ('-' for stdin)")
	fs.StringVar(&r.name, "name", "", "rule name")
	fs.StringVar(&r.kind, "kind", "", "signal kind: metric, log, trace")
	fs.StringVar(&r.expression, "expression", "", "selector in the kind's query language")
	fs.StringVar(&r.demandID, "demand", "", "demand id this rule belongs to")
	fs.BoolVar(&r.enabled, "enabled", true, "whether the rule forwards its signal")
}

func (r *ruleBodyFlags) body(cmd *cobra.Command) ([]byte, error) {
	var base []byte

	if r.file != "" {
		var err error
		if base, err = readBody(r.file); err != nil {
			return nil, err
		}
	}

	overrides := map[string]any{}
	putIf(overrides, "name", r.name)
	putIf(overrides, "kind", r.kind)
	putIf(overrides, "expression", r.expression)
	putIf(overrides, "demand_id", r.demandID)

	if cmd.Flags().Changed("enabled") {
		overrides["enabled"] = r.enabled
	}

	return mergeBody(base, overrides)
}

func newRuleCreateCommand(f *Factory) *cobra.Command {
	flags := &ruleBodyFlags{}

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a custom rule",
		Example: `  leanctl rule create --name nginx-logs --kind log \
    --expression '{job="nginx"}' --demand <demand-id>`,
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

			raw, err := api.Post(ctx, "/custom_rule", nil, body)
			if err != nil {
				return err
			}

			var created client.CustomRule
			if err := jsonUnmarshal(raw, &created); err != nil {
				return err
			}

			return emitAction(f, raw, "Created custom rule %q (%s)", created.Name, created.ID)
		},
	}

	flags.bind(cmd)

	return cmd
}

func newRuleUpdateCommand(f *Factory) *cobra.Command {
	flags := &ruleBodyFlags{}

	cmd := &cobra.Command{
		Use:   "update <name|id>",
		Short: "Update a custom rule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, customRulesPath, args[0])
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

			raw, err := api.Put(ctx, "/custom_rule/"+id, nil, body)
			if err != nil {
				return err
			}

			return emitAction(f, raw, "Updated custom rule %s", args[0])
		},
	}

	flags.bind(cmd)

	return cmd
}

func newRuleDeleteCommand(f *Factory) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     "delete <name|id>",
		Aliases: []string{"rm"},
		Short:   "Delete a custom rule",
		Long: `Delete a custom rule.

Whatever it was the only demand for stops being stored, and the existing data is
removed after the tenant's purge grace window.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, customRulesPath, args[0])
			if err != nil {
				return err
			}

			if err := confirm(cmd, yes, "delete custom rule %q", args[0]); err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			if err := api.Delete(ctx, "/custom_rule/"+id, nil); err != nil {
				return err
			}

			return emitAction(f, nil, "Deleted custom rule %s", args[0])
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

func newRuleStateCommand(f *Factory, action, short string) *cobra.Command {
	return &cobra.Command{
		Use:   action + " <name|id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, customRulesPath, args[0])
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			raw, err := api.Post(ctx, "/custom_rule/"+id+"/"+action, nil, nil)
			if err != nil {
				return err
			}

			return emitAction(f, raw, "%sd custom rule %s", capitalize(action), args[0])
		},
	}
}
