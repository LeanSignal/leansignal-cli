package cli

import (
	"net/url"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/leansignal/lean-cli/internal/output"
)

func newLogsCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "logs",
		Aliases: []string{"log"},
		Short:   "Query logs",
		Long: `Query logs on either side of the demand line.

The central store keeps only the log streams a demand matches, for 30 days. The
agent's local store keeps everything it collects for about an hour: use
--available to see what a demand could pick up.

Queries are LogQL. There is no live tail — Loki's tail endpoint needs a
WebSocket, which the query proxy does not carry.`,
	}

	cmd.AddCommand(
		newLogsQueryCommand(f),
		newLogsLabelsCommand(f),
		newLogsLabelValuesCommand(f),
		newLogsStatsCommand(f),
	)

	return cmd
}

func lokiProxyGet(
	cmd *cobra.Command, f *Factory, available bool, suffix string, q url.Values,
) ([]byte, error) {
	ctx, cancel := cmdContext(cmd)
	defer cancel()

	api, err := f.Client()
	if err != nil {
		return nil, err
	}

	return api.Get(ctx, logsProxy.path(available, suffix), q)
}

func newLogsQueryCommand(f *Factory) *cobra.Command {
	var (
		available bool
		tr        timeRangeFlags
		direction string
	)

	cmd := &cobra.Command{
		Use:   "query <logql>",
		Short: "Run a LogQL range query",
		Example: `  leanctl logs query '{job="nginx"}' --start now-30m
  leanctl logs query '{job="nginx"} |= "error"' --available`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			start, err := parseTime(orElse(tr.start, "now-1h"))
			if err != nil {
				return err
			}

			end, err := parseTime(orElse(tr.end, "now"))
			if err != nil {
				return err
			}

			limit := tr.limit
			if limit <= 0 {
				limit = 100
			}

			q := url.Values{
				"query":     {args[0]},
				"start":     {start + "000000000"},
				"end":       {end + "000000000"},
				"limit":     {strconv.Itoa(limit)},
				"direction": {orElse(direction, "backward")},
			}

			raw, err := lokiProxyGet(cmd, f, available, "/loki/api/v1/query_range", q)
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				var resp struct {
					Data struct {
						ResultType string `json:"resultType"`
						Result     []struct {
							Stream map[string]string `json:"stream"`
							Values [][]string        `json:"values"`
						} `json:"result"`
					} `json:"data"`
				}

				if err := jsonUnmarshal(raw, &resp); err != nil {
					return nil, err
				}

				t := output.NewTable("TIMESTAMP", "STREAM", "LINE")
				if !available {
					t.Empty = emptyStoredHint(logSignal, args[0])
				} else {
					t.Empty = "No log lines at the agent for that selector and window."
				}

				for _, stream := range resp.Data.Result {
					labels := formatLabels(stream.Stream)

					for _, entry := range stream.Values {
						if len(entry) < 2 {
							continue
						}

						t.Add(formatLokiTimestamp(entry[0]), labels, entry[1])
					}
				}

				return t, nil
			})
		},
	}

	fs := cmd.Flags()
	fs.BoolVar(&available, "available", false, availableFlagHelp)
	fs.StringVar(&tr.start, "start", "now-1h", "range start (RFC3339 or now-1h)")
	fs.StringVar(&tr.end, "end", "now", "range end")
	fs.IntVar(&tr.limit, "limit", 100, "maximum log lines to return")
	fs.StringVar(&direction, "direction", "backward", "backward (newest first) or forward")

	return cmd
}

func newLogsLabelsCommand(f *Factory) *cobra.Command {
	var available bool

	cmd := &cobra.Command{
		Use:   "labels",
		Short: "List log label names",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := lokiProxyGet(cmd, f, available, "/loki/api/v1/labels", nil)
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				var resp struct {
					Data []string `json:"data"`
				}

				if err := jsonUnmarshal(raw, &resp); err != nil {
					return nil, err
				}

				t := output.NewTable("LABEL")
				if !available {
					t.Empty = emptyStoredHint(logSignal, "")
				} else {
					t.Empty = "The agent reports no log labels. Is it connected?"
				}

				for _, l := range resp.Data {
					t.Add(l)
				}

				return t, nil
			})
		},
	}

	cmd.Flags().BoolVar(&available, "available", false, availableFlagHelp)

	return cmd
}

func newLogsLabelValuesCommand(f *Factory) *cobra.Command {
	var available bool

	cmd := &cobra.Command{
		Use:     "label-values <label>",
		Short:   "List the values of a log label",
		Example: `  leanctl logs label-values job`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			suffix := "/loki/api/v1/label/" + url.PathEscape(args[0]) + "/values"

			raw, err := lokiProxyGet(cmd, f, available, suffix, nil)
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				var resp struct {
					Data []string `json:"data"`
				}

				if err := jsonUnmarshal(raw, &resp); err != nil {
					return nil, err
				}

				t := output.NewTable("VALUE")
				t.Empty = "No values for label " + strconv.Quote(args[0]) + "."

				for _, v := range resp.Data {
					t.Add(v)
				}

				return t, nil
			})
		},
	}

	cmd.Flags().BoolVar(&available, "available", false, availableFlagHelp)

	return cmd
}

func newLogsStatsCommand(f *Factory) *cobra.Command {
	var tr timeRangeFlags

	cmd := &cobra.Command{
		Use:   "stats <logql>",
		Short: "Show volume statistics for a selector (stored only)",
		Long: `Report how much a selector matches in the central store.

Run this before pulling lines: it answers "is this worth querying" without
transferring the log bodies. Only the stored side implements it.`,
		Example: `  leanctl logs stats '{job="nginx"}' --start now-24h`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			start, err := parseTime(orElse(tr.start, "now-1h"))
			if err != nil {
				return err
			}

			end, err := parseTime(orElse(tr.end, "now"))
			if err != nil {
				return err
			}

			q := url.Values{
				"query": {args[0]},
				"start": {start + "000000000"},
				"end":   {end + "000000000"},
			}

			raw, err := lokiProxyGet(cmd, f, false, "/loki/api/v1/index/stats", q)
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				var resp struct {
					Streams int64 `json:"streams"`
					Chunks  int64 `json:"chunks"`
					Entries int64 `json:"entries"`
					Bytes   int64 `json:"bytes"`
				}

				if err := jsonUnmarshal(raw, &resp); err != nil {
					return nil, err
				}

				t := output.NewTable("STREAMS", "CHUNKS", "ENTRIES", "BYTES")
				t.Add(resp.Streams, resp.Chunks, resp.Entries, output.Bytes(resp.Bytes))

				return t, nil
			})
		},
	}

	cmd.Flags().StringVar(&tr.start, "start", "now-1h", "range start")
	cmd.Flags().StringVar(&tr.end, "end", "now", "range end")

	return cmd
}

// formatLokiTimestamp converts Loki's nanosecond string to RFC3339.
func formatLokiTimestamp(ns string) string {
	n, err := strconv.ParseInt(ns, 10, 64)
	if err != nil {
		return ns
	}

	return time.Unix(0, n).UTC().Format("2006-01-02T15:04:05.000Z")
}
