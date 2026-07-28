package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/leansignal/leansignal-cli/internal/build"
)

func newVersionCommand() *cobra.Command {
	var short bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the leanctl version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			if short {
				fmt.Fprintln(out, build.Version)

				return nil
			}

			fmt.Fprintf(out, "%s\n", build.Info())
			fmt.Fprintf(out, "  go:       %s\n", build.GoVersion())
			fmt.Fprintf(out, "  platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)

			return nil
		},
	}

	cmd.Flags().BoolVar(&short, "short", false, "print only the version number")

	return cmd
}

func newCompletionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion <bash|zsh|fish|powershell>",
		Short: "Generate a shell completion script",
		Long: `Generate a shell completion script.

  bash:  leanctl completion bash > /etc/bash_completion.d/leanctl
  zsh:   leanctl completion zsh > "${fpath[1]}/_leanctl"
  fish:  leanctl completion fish > ~/.config/fish/completions/leanctl.fish`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletionV2(out, true)
			case "zsh":
				return cmd.Root().GenZshCompletion(out)
			case "fish":
				return cmd.Root().GenFishCompletion(out, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(out)
			default:
				return fmt.Errorf("unsupported shell %q", args[0])
			}
		},
	}

	return cmd
}
