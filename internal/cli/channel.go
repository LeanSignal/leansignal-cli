package cli

import (
	"github.com/spf13/cobra"

	"github.com/leansignal/leansignal-cli/internal/client"
	"github.com/leansignal/leansignal-cli/internal/output"
)

const channelsPath = "/notification_channels"

func newChannelCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "channel",
		Aliases: []string{"channels"},
		Short:   "Manage notification channels",
		Long: `A notification channel is where alerts and synthetic checks deliver: an email
recipient list or a Slack webhook.

Email delivery goes through control-center, which filters to recipients who have
verified their address and not opted out.`,
	}

	cmd.AddCommand(
		newChannelListCommand(f),
		newChannelGetCommand(f),
		newChannelCreateCommand(f),
		newChannelUpdateCommand(f),
		newChannelDeleteCommand(f),
		newChannelTestCommand(f),
	)

	return cmd
}

func newChannelListCommand(f *Factory) *cobra.Command {
	var opts listFlags

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List notification channels",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd, f, channelsPath, opts.options(),
				func(items []client.NotificationChannel, wide bool) *output.Table {
					headers := []string{"NAME", "TYPE", "ENABLED", "CREATED BY", "AGE"}
					if wide {
						headers = append([]string{"ID"}, headers...)
					}

					t := output.NewTable(headers...)
					t.Empty = "No notification channels."

					for _, c := range items {
						if wide {
							t.Add(c.ID, c.Name, c.Type, c.Enabled, c.CreatedByEmail, c.CreatedAt)

							continue
						}

						t.Add(c.Name, c.Type, c.Enabled, c.CreatedByEmail, c.CreatedAt)
					}

					return t
				})
		},
	}

	opts.bind(cmd)

	return cmd
}

func newChannelGetCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <name|id>",
		Short: "Show one notification channel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveID(cmd, f, channelsPath, args[0])
			if err != nil {
				return err
			}

			return runGet(cmd, f, "/notification_channel/"+id, nil,
				func(c client.NotificationChannel, _ bool) *output.Table {
					t := output.NewTable("FIELD", "VALUE")
					t.Add("id", c.ID)
					t.Add("name", c.Name)
					t.Add("type", c.Type)
					t.Add("enabled", c.Enabled)
					t.Add("created_by", c.CreatedByEmail)

					return t
				})
		},
	}

	return cmd
}

type channelBodyFlags struct {
	file       string
	name       string
	kind       string
	recipients []string
	webhookURL string
	enabled    bool
}

func (c *channelBodyFlags) bind(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringVarP(&c.file, "file", "f", "", "JSON body ('-' for stdin)")
	fs.StringVar(&c.name, "name", "", "channel name")
	fs.StringVar(&c.kind, "type", "", "channel type: email, slack")
	fs.StringArrayVar(&c.recipients, "recipient", nil, "email recipient (repeatable, email channels)")
	fs.StringVar(&c.webhookURL, "webhook-url", "", "Slack webhook URL (slack channels)")
	fs.BoolVar(&c.enabled, "enabled", true, "whether the channel delivers")
}

func (c *channelBodyFlags) body(cmd *cobra.Command) ([]byte, error) {
	var base []byte

	if c.file != "" {
		var err error
		if base, err = readBody(c.file); err != nil {
			return nil, err
		}
	}

	overrides := map[string]any{}
	putIf(overrides, "name", c.name)
	putIf(overrides, "type", c.kind)
	putIf(overrides, "webhook_url", c.webhookURL)

	if len(c.recipients) > 0 {
		overrides["recipients"] = c.recipients
	}

	if cmd.Flags().Changed("enabled") {
		overrides["enabled"] = c.enabled
	}

	return mergeBody(base, overrides)
}

func newChannelCreateCommand(f *Factory) *cobra.Command {
	flags := &channelBodyFlags{}

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a notification channel",
		Example: `  leanctl channel create --name oncall --type email --recipient ops@example.com
  leanctl channel create --name alerts --type slack --webhook-url https://hooks.slack.com/...`,
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

			raw, err := api.Post(ctx, "/notification_channel", nil, body)
			if err != nil {
				return err
			}

			var created client.NotificationChannel
			if err := jsonUnmarshal(raw, &created); err != nil {
				return err
			}

			return emitAction(f, raw, "Created channel %q (%s)", created.Name, created.ID)
		},
	}

	flags.bind(cmd)

	return cmd
}

func newChannelUpdateCommand(f *Factory) *cobra.Command {
	flags := &channelBodyFlags{}

	cmd := &cobra.Command{
		Use:   "update <name|id>",
		Short: "Update a notification channel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, channelsPath, args[0])
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

			raw, err := api.Put(ctx, "/notification_channel/"+id, nil, body)
			if err != nil {
				return err
			}

			return emitAction(f, raw, "Updated channel %s", args[0])
		},
	}

	flags.bind(cmd)

	return cmd
}

func newChannelDeleteCommand(f *Factory) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     "delete <name|id>",
		Aliases: []string{"rm"},
		Short:   "Delete a notification channel",
		Long: `Delete a notification channel.

Alert rules that relied on it lose a delivery target; a rule left with no
channel at all stops notifying.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, channelsPath, args[0])
			if err != nil {
				return err
			}

			if err := confirm(cmd, yes, "delete channel %q", args[0]); err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			if err := api.Delete(ctx, "/notification_channel/"+id, nil); err != nil {
				return err
			}

			return emitAction(f, nil, "Deleted channel %s", args[0])
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")

	return cmd
}

func newChannelTestCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test <name|id>",
		Short: "Send a test notification",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := cmdContext(cmd)
			defer cancel()

			id, err := resolveID(cmd, f, channelsPath, args[0])
			if err != nil {
				return err
			}

			api, err := f.Client()
			if err != nil {
				return err
			}

			raw, err := api.Post(ctx, "/notification_channel/"+id+"/test", nil, nil)
			if err != nil {
				return err
			}

			return emitAction(f, raw, "Test notification sent to %s.", args[0])
		},
	}

	return cmd
}
