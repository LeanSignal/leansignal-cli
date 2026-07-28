// Package cli builds leanctl's command tree.
//
// The shape is the conventional one — `leanctl <noun> <verb> [name] [flags]` —
// so that anything learned from kubectl, gh, or doctl transfers directly. Every
// noun matches a section of the LeanSignal web app, and every verb maps to one
// lean-api endpoint.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/leansignal/leansignal-cli/internal/build"
	"github.com/leansignal/leansignal-cli/internal/client"
	"github.com/leansignal/leansignal-cli/internal/config"
	"github.com/leansignal/leansignal-cli/internal/output"
)

// Factory lazily resolves the settings, API client, and printer a command
// needs. Commands that touch no API (version, completion, context) never
// trigger credential resolution.
type Factory struct {
	overrides config.Overrides
	noHeaders bool

	settings *config.Settings
	api      *client.Client
	printer  *output.Printer
}

// Settings resolves flags, environment, and config file exactly once.
func (f *Factory) Settings() (*config.Settings, error) {
	if f.settings != nil {
		return f.settings, nil
	}

	s, err := config.Resolve(f.overrides)
	if err != nil {
		return nil, err
	}

	f.settings = s

	return s, nil
}

// Client returns an authenticated API client, or a clear error explaining how
// to obtain a credential.
func (f *Factory) Client() (*client.Client, error) {
	if f.api != nil {
		return f.api, nil
	}

	s, err := f.Settings()
	if err != nil {
		return nil, err
	}

	c, err := client.New(s, "leanctl/"+build.Version)
	if err != nil {
		return nil, err
	}

	f.api = c

	return c, nil
}

// Printer returns the configured output renderer.
func (f *Factory) Printer() (*output.Printer, error) {
	if f.printer != nil {
		return f.printer, nil
	}

	s, err := f.Settings()
	if err != nil {
		return nil, err
	}

	format, err := output.ParseFormat(s.Output)
	if err != nil {
		return nil, &client.UsageError{Msg: err.Error()}
	}

	f.printer = output.New(format, f.noHeaders, !s.NoColor && isTerminal(os.Stdout))

	return f.printer, nil
}

// NewRootCommand assembles the full command tree.
func NewRootCommand() *cobra.Command {
	f := &Factory{}

	root := &cobra.Command{
		Use:   "leanctl",
		Short: "Control LeanSignal from the command line",
		Long: `leanctl is the command-line client for LeanSignal.

It authenticates with a personal access token minted in the web app
(Preferences -> Access tokens) and acts with your own identity and role, so it
can do what you can do in the UI — no more.

LeanSignal stores only demanded telemetry. Query commands therefore have two
sides: the default reads the central store (demanded data, 30d retention), and
--available reads the edge agent's local store (everything it collects, ~1d for
metrics and ~1h for logs and traces).`,
		Example: `  # Log in to a tenant (prompts for the token)
  leanctl auth login --tenant petkopuma

  # List what you have
  leanctl demand list
  leanctl agent list

  # Export a demand and its dashboards + alerts, then re-import elsewhere
  leanctl demand export host-metrics > host-metrics.json
  leanctl demand import --file host-metrics.json --dry-run

  # Is a log stream stored, or merely available at the agent?
  leanctl logs query '{job="nginx"}'
  leanctl logs query '{job="nginx"}' --available`,
		SilenceUsage:      true,
		SilenceErrors:     true,
		DisableAutoGenTag: true,
		Version:           build.Version,

		// Validate global flags before any command runs, so `-o toml` fails as
		// a usage error rather than as whatever the command hits first.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Name() == "help" || cmd.Name() == "completion" {
				return nil
			}

			if f.overrides.Output != "" {
				if _, err := output.ParseFormat(f.overrides.Output); err != nil {
					return &client.UsageError{Msg: err.Error()}
				}
			}

			return nil
		},
	}

	p := root.PersistentFlags()
	p.StringVar(&f.overrides.ConfigPath, "config", "",
		"config file (default "+config.DefaultPath()+")")
	p.StringVar(&f.overrides.ContextName, "context", "", "context to use (default: current_context)")
	p.StringVar(&f.overrides.APIURL, "api-url", "", "tenant API origin (overrides the context)")
	p.StringVarP(&f.overrides.Output, "output", "o", "",
		"output format: "+joinFormats())
	p.BoolVar(&f.noHeaders, "no-headers", false, "omit table headers")
	p.DurationVar(&f.overrides.Timeout, "timeout", 0,
		"per-request timeout (default "+config.DefaultTimeout.String()+")")
	p.BoolVarP(&f.overrides.Verbose, "verbose", "v", false, "log requests to stderr")
	p.BoolVar(&f.overrides.NoColor, "no-color", false, "disable coloured output")

	// There is deliberately no --token flag: a command line is visible to every
	// process on the host and lands in shell history. Use `leanctl auth login`
	// (prompt or --token-stdin) or the LEANCTL_TOKEN environment variable.

	root.AddCommand(
		newAuthCommand(f),
		newContextCommand(f),
		newDemandCommand(f),
		newDashboardCommand(f),
		newAgentCommand(f),
		newAlertCommand(f),
		newChannelCommand(f),
		newSyntheticCommand(f),
		newRuleCommand(f),
		newFilterCommand(f),
		newMetricsCommand(f),
		newLogsCommand(f),
		newTracesCommand(f),
		newStatusCommand(f),
		newAuditCommand(f),
		newSettingsCommand(f),
		newSearchCommand(f),
		newVersionCommand(),
		newCompletionCommand(),
	)

	root.SetVersionTemplate("leanctl {{.Version}}\n")

	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	root := NewRootCommand()

	if err := root.Execute(); err != nil {
		code := client.ExitCode(err)

		var apiErr *client.APIError
		if ok := asAPIError(err, &apiErr); ok {
			fmt.Fprintf(os.Stderr, "Error: %s\n", apiErr.Error())

			if hint := apiErr.Hint(); hint != "" {
				fmt.Fprintf(os.Stderr, "  %s\n", hint)
			}

			if apiErr.Details != nil {
				fmt.Fprintf(os.Stderr, "  details: %v\n", apiErr.Details)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}

		return code
	}

	return client.ExitOK
}

func joinFormats() string {
	out := ""

	for i, s := range output.Formats {
		if i > 0 {
			out += "|"
		}

		out += s
	}

	return out
}

// isTerminal reports whether f is an interactive terminal, so colour and
// prompts can be suppressed when output is piped.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}
