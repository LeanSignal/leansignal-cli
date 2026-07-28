package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/leansignal/lean-cli/internal/client"
	"github.com/leansignal/lean-cli/internal/output"
)

// The collector config lives on the agent host, not in lean-api's database.
// These two commands ride the gRPC control stream: lean-api asks the connected
// agent for its on-disk config, or pushes an edited file back. The agent — not
// lean-api and not leanctl — validates the result, so a config that would not
// start the collector is refused before anything is written.
func newAgentConfigCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and edit a connected agent's collector config (admin)",
		Long: `Read or replace the OpenTelemetry Collector configuration on an agent host.

Both require the admin role and a connected agent — the config is fetched from
and written to the host over the agent's control stream, so a disconnected agent
answers 503.

The agent validates every write (YAML parse plus a full collector dry-run of the
merged config) and applies it only if it passes: the previous file is kept as
<path>.bak, the write is atomic, and the collector reloads. A rejected config is
never written, and the validator's own complaint comes back verbatim.

The intended loop:

  leanctl agent config get lsh --path /etc/leansignal-agent/config.yaml > c.yaml
  $EDITOR c.yaml
  leanctl agent config apply lsh --file c.yaml`,
	}

	cmd.AddCommand(newAgentConfigGetCommand(f), newAgentConfigApplyCommand(f))

	return cmd
}

func newAgentConfigGetCommand(f *Factory) *cobra.Command {
	var path string

	cmd := &cobra.Command{
		Use:   "get <name|id>",
		Short: "Show the agent's collector config",
		Long: `List the config sources the collector was started with, or emit one of them.

Without --path a summary is shown: every source in --config order (later files
override earlier ones), which of them can be written, and the file a write
targets by default. With --path the file's contents go to stdout verbatim, ready
to redirect into an editor.

Values are unresolved: ${env:...} and ${leansignal:...} references are shown as
written, so nothing the config merely references is exposed.`,
		Example: `  leanctl agent config get lsh
  leanctl agent config get lsh --path /etc/leansignal-agent/config.yaml > c.yaml`,
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

			var cfg client.AgentConfig

			raw, err := api.GetInto(ctx, "/agent/config/"+id, nil, &cfg)
			if err != nil {
				return err
			}

			if cfg.Error != "" {
				return fmt.Errorf("the agent could not read its config: %s", cfg.Error)
			}

			// --path emits the file itself, not a description of it.
			if path != "" {
				file := cfg.File(path)
				if file == nil {
					return client.Usage("agent %q has no config source %q; known paths: %s",
						args[0], path, cfg.Paths())
				}

				if file.Error != "" {
					return fmt.Errorf("the agent could not read %s: %s", file.Path, file.Error)
				}

				_, werr := cmd.OutOrStdout().Write(ensureTrailingNewline([]byte(file.Content)))

				return werr
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				t := output.NewTable("PATH", "WRITABLE", "BYTES", "NOTE")
				t.Empty = "The agent reported no config sources."

				for _, file := range cfg.Files {
					note := file.Error
					if note == "" && file.Path == cfg.PrimaryPath {
						note = "default write target"
					}

					t.Add(file.Path, file.Writable, len(file.Content), note)
				}

				if !cfg.WriteEnabled {
					t.Add("", "", "", "remote config writes are disabled on this agent")
				}

				return t, nil
			})
		},
	}

	cmd.Flags().StringVar(&path, "path", "", "emit this file's contents instead of the summary")

	return cmd
}

func newAgentConfigApplyCommand(f *Factory) *cobra.Command {
	var (
		file string
		path string
		yes  bool
	)

	cmd := &cobra.Command{
		Use:   "apply <name|id>",
		Short: "Replace a config file on the agent and reload it",
		Long: `Push a config file to the agent.

The agent validates it and, only if it passes, writes it atomically (keeping the
previous contents as <path>.bak) and reloads the collector. A rejected config
changes nothing on the host and exits 5 with the validator's reason.

--path selects which source to write and must be one of the paths from
'agent config get'; omitted, it writes that command's default target.`,
		Example: `  leanctl agent config apply lsh --file c.yaml
  leanctl agent config apply lsh -f c.yaml --path /etc/leansignal-agent/config.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			content, err := readRaw(file)
			if err != nil {
				return err
			}

			id, err := resolveID(cmd, f, agentsPath, args[0])
			if err != nil {
				return err
			}

			target := path
			if target == "" {
				target = "its default config file"
			}

			// A reload interrupts collection on a live host, so this asks first
			// even though the agent keeps a .bak — the prompt is about the
			// restart, not about losing the old file.
			if err := confirm(cmd, yes,
				"replace %s on agent %q and reload the collector", target, args[0]); err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			body := mustJSON(client.AgentConfigUpdate{Path: path, Content: string(content)})

			raw, err := api.Put(ctx, "/agent/config/"+id, nil, body)
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

			var res client.AgentConfigUpdateResult
			if err := jsonUnmarshal(raw, &res); err != nil {
				return err
			}

			// The agent's own message is the useful part — on both outcomes it
			// explains what it did or why it refused — so it is printed verbatim.
			if !res.Applied {
				return &client.APIError{
					Status:  422,
					Code:    "config_rejected",
					Message: res.Message,
				}
			}

			p.Message("%s", res.Message)

			return nil
		},
	}

	fs := cmd.Flags()
	fs.StringVarP(&file, "file", "f", "", "config file to upload ('-' for stdin)")
	fs.StringVar(&path, "path", "", "which config source to write (default: the agent's primary)")
	fs.BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	if err := cmd.MarkFlagRequired("file"); err != nil {
		panic(err)
	}

	return cmd
}

func ensureTrailingNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] != '\n' {
		return append(b, '\n')
	}

	return b
}
