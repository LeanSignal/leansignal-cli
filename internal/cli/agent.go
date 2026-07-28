package cli

import (
	"github.com/spf13/cobra"

	"github.com/leansignal/leansignal-cli/internal/client"
	"github.com/leansignal/leansignal-cli/internal/output"
)

const agentsPath = "/agents"

func newAgentCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "agent",
		Aliases: []string{"agents"},
		Short:   "Manage edge agents",
		Long: `An agent is the edge collector for one host or cluster.

It dials out to LeanSignal over a single long-lived gRPC stream, keeps full
fidelity locally, and forwards only the demanded subset. Exactly one agent may
be connected per tenant.

Most of these manage the agent record in LeanSignal, not the process — installing
and restarting an agent happens on its host. The exception is 'agent config',
which reads and rewrites the collector configuration on the host itself, over the
agent's own control stream.`,
	}

	cmd.AddCommand(
		newAgentListCommand(f),
		newAgentGetCommand(f),
		newAgentCreateCommand(f),
		newAgentUpdateCommand(f),
		newAgentDeleteCommand(f),
		newAgentDiagnoseCommand(f),
		newAgentConfigCommand(f),
	)

	return cmd
}

func agentTable(items []client.Agent, wide bool) *output.Table {
	headers := []string{"NAME", "STATUS", "VERSION", "COLLECTED", "STORED", "LAST SEEN"}
	if wide {
		headers = append([]string{"ID"}, append(headers, "DEMAND UPDATED")...)
	}

	t := output.NewTable(headers...)
	t.Empty = "No agents. Install one with the instructions at docs.leansignal.io."

	for _, a := range items {
		if wide {
			t.Add(a.ID, a.Name, a.Status, a.Version,
				a.TimeseriesCollectedAVM, a.TimeseriesCollectedDP, a.LastTimeSeen, a.DemandLastUpdate)

			continue
		}

		t.Add(a.Name, a.Status, a.Version,
			a.TimeseriesCollectedAVM, a.TimeseriesCollectedDP, a.LastTimeSeen)
	}

	return t
}

func newAgentListCommand(f *Factory) *cobra.Command {
	var opts listFlags

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List agents",
		Long: `List agents.

COLLECTED is how many time series the agent sees locally; STORED is how many it
forwards centrally. A large gap is the demand-driven model working as intended,
not a fault.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd, f, agentsPath, opts.options(), agentTable)
		},
	}

	opts.bind(cmd)

	return cmd
}

func newAgentGetCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <name|id>",
		Short: "Show one agent",
		Long: `Show one agent.

agent_key is returned only to admins; for any other role the field is absent
from the response entirely.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveID(cmd, f, agentsPath, args[0])
			if err != nil {
				return err
			}

			return runGet(cmd, f, "/agent/"+id, nil, func(a client.Agent, _ bool) *output.Table {
				t := output.NewTable("FIELD", "VALUE")
				t.Add("id", a.ID)
				t.Add("name", a.Name)
				t.Add("status", a.Status)
				t.Add("version", a.Version)
				t.Add("collected (local)", a.TimeseriesCollectedAVM)
				t.Add("stored (central)", a.TimeseriesCollectedDP)
				t.Add("last_seen", a.LastTimeSeen)
				t.Add("demand_last_update", a.DemandLastUpdate)

				if a.AgentKey != nil {
					t.Add("agent_key", *a.AgentKey)
				}

				return t
			})
		},
	}

	return cmd
}

func newAgentCreateCommand(f *Factory) *cobra.Command {
	var (
		name string
		file string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Register an agent",
		Long: `Register an agent and mint its key.

A tenant may have exactly one agent; a second registration is refused. The key
is shown to admins only — put it in /etc/leansignal-agent/agent.env on the host.`,
		Args: cobra.NoArgs,
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

			body, err := mergeBody(base, overrides)
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			raw, err := api.Post(ctx, "/agent", nil, body)
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			if p.Structured() {
				return p.Emit(raw, nil)
			}

			var created client.Agent
			if err := jsonUnmarshal(raw, &created); err != nil {
				return err
			}

			p.Message("Created agent %q (%s)", created.Name, created.ID)

			if created.AgentKey != nil {
				p.Message("Agent key: %s", *created.AgentKey)
				p.Note("Store the key now — it authenticates every plane (gRPC, metrics, logs, traces).")
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "agent name")
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON body ('-' for stdin)")

	return cmd
}

func newAgentUpdateCommand(f *Factory) *cobra.Command {
	var (
		name string
		file string
	)

	cmd := &cobra.Command{
		Use:   "update <name|id>",
		Short: "Update an agent record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, agentsPath, args[0])
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

			body, err := mergeBody(base, overrides)
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			raw, err := api.Put(ctx, "/agent/"+id, nil, body)
			if err != nil {
				return err
			}

			return emitAction(f, raw, "Updated agent %s", args[0])
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "new agent name")
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON body ('-' for stdin)")

	return cmd
}

func newAgentDeleteCommand(f *Factory) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     "delete <name|id>",
		Aliases: []string{"rm"},
		Short:   "Delete an agent record",
		Long: `Delete an agent record and invalidate its key.

The agent process keeps running on its host; it will simply fail to
authenticate on its next connection attempt.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, agentsPath, args[0])
			if err != nil {
				return err
			}

			if err := confirm(cmd, yes, "delete agent %q and invalidate its key", args[0]); err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			if err := api.Delete(ctx, "/agent/"+id, nil); err != nil {
				return err
			}

			return emitAction(f, nil, "Deleted agent %s", args[0])
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

func newAgentDiagnoseCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diagnose <name|id>",
		Short: "Ask a connected agent to log a diagnosis (admin)",
		Long: `Push a diagnosis command down the agent's control stream.

The agent writes the result to its own log — read it there with
'journalctl -u leansignal-agent'. Requires an admin role and a connected agent.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, agentsPath, args[0])
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			raw, err := api.Get(ctx, "/agent/diagnosis/"+id, nil)
			if err != nil {
				return err
			}

			return emitAction(f, raw,
				"Diagnosis requested. Read it on the agent host: journalctl -u leansignal-agent -n 200")
		},
	}

	return cmd
}
