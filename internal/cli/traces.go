package cli

import (
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/leansignal/lean-cli/internal/output"
)

func newTracesCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "traces",
		Aliases: []string{"trace"},
		Short:   "Query traces",
		Long: `Query traces on either side of the demand line.

The central store keeps spans from resources a demand selects, for 30 days; the
agent keeps everything it receives for about an hour. Use --available to see
what could be demanded.`,
	}

	cmd.AddCommand(
		newTracesSearchCommand(f),
		newTracesGetCommand(f),
		newTracesTagsCommand(f),
	)

	return cmd
}

func tempoProxyGet(
	cmd *cobra.Command, f *Factory, available bool, suffix string, q url.Values,
) ([]byte, error) {
	ctx, cancel := cmdContext(cmd)
	defer cancel()

	api, err := f.Client()
	if err != nil {
		return nil, err
	}

	return api.Get(ctx, tracesProxy.path(available, suffix), q)
}

func newTracesSearchCommand(f *Factory) *cobra.Command {
	var (
		available bool
		tr        timeRangeFlags
		tags      string
		traceQL   string
		minDur    string
		maxDur    string
	)

	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search for traces",
		Example: `  leanctl traces search --tags 'service.name=lean-api' --start now-1h
  leanctl traces search --query '{ duration > 500ms }' --available`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
				limit = 20
			}

			q := url.Values{
				"start": {start},
				"end":   {end},
				"limit": {strconv.Itoa(limit)},
			}

			if tags != "" {
				q.Set("tags", tags)
			}

			if traceQL != "" {
				q.Set("q", traceQL)
			}

			if minDur != "" {
				q.Set("minDuration", minDur)
			}

			if maxDur != "" {
				q.Set("maxDuration", maxDur)
			}

			raw, err := tempoProxyGet(cmd, f, available, "/api/search", q)
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				var resp struct {
					Traces []struct {
						TraceID           string `json:"traceID"`
						RootServiceName   string `json:"rootServiceName"`
						RootTraceName     string `json:"rootTraceName"`
						DurationMs        int    `json:"durationMs"`
						StartTimeUnixNano string `json:"startTimeUnixNano"`
					} `json:"traces"`
				}

				if err := jsonUnmarshal(raw, &resp); err != nil {
					return nil, err
				}

				t := output.NewTable("TRACE ID", "SERVICE", "NAME", "DURATION(ms)", "START")
				if !available {
					t.Empty = emptyStoredHint(traceSignal, traceQL)
				} else {
					t.Empty = "No traces at the agent for that window."
				}

				for _, tr := range resp.Traces {
					t.Add(tr.TraceID, tr.RootServiceName, tr.RootTraceName, tr.DurationMs,
						formatLokiTimestamp(tr.StartTimeUnixNano))
				}

				return t, nil
			})
		},
	}

	fs := cmd.Flags()
	fs.BoolVar(&available, "available", false, availableFlagHelp)
	fs.StringVar(&tags, "tags", "", "tag filter, e.g. 'service.name=lean-api'")
	fs.StringVar(&traceQL, "query", "", "TraceQL query")
	fs.StringVar(&tr.start, "start", "now-1h", "range start")
	fs.StringVar(&tr.end, "end", "now", "range end")
	fs.IntVar(&tr.limit, "limit", 20, "maximum traces to return")
	fs.StringVar(&minDur, "min-duration", "", "only traces longer than this, e.g. 500ms")
	fs.StringVar(&maxDur, "max-duration", "", "only traces shorter than this")

	return cmd
}

func newTracesGetCommand(f *Factory) *cobra.Command {
	var available bool

	cmd := &cobra.Command{
		Use:   "get <trace-id>",
		Short: "Fetch a whole trace",
		Long: `Fetch a trace by id.

The full document is large; table output summarises the spans, so use -o json
when you want the trace itself.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := tempoProxyGet(cmd, f, available, "/api/traces/"+url.PathEscape(args[0]), nil)
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				var resp struct {
					Batches []struct {
						Resource struct {
							Attributes []struct {
								Key   string `json:"key"`
								Value struct {
									StringValue string `json:"stringValue"`
								} `json:"value"`
							} `json:"attributes"`
						} `json:"resource"`
						ScopeSpans []struct {
							Spans []struct {
								Name              string `json:"name"`
								SpanID            string `json:"spanId"`
								StartTimeUnixNano string `json:"startTimeUnixNano"`
								EndTimeUnixNano   string `json:"endTimeUnixNano"`
							} `json:"spans"`
						} `json:"scopeSpans"`
					} `json:"batches"`
				}

				if err := jsonUnmarshal(raw, &resp); err != nil {
					return nil, err
				}

				t := output.NewTable("SERVICE", "SPAN", "SPAN ID", "DURATION(ms)")
				t.Empty = "Trace not found, or it holds no spans."

				for _, batch := range resp.Batches {
					service := "-"

					for _, attr := range batch.Resource.Attributes {
						if attr.Key == "service.name" {
							service = attr.Value.StringValue
						}
					}

					for _, scope := range batch.ScopeSpans {
						for _, span := range scope.Spans {
							t.Add(service, span.Name, span.SpanID,
								spanDurationMS(span.StartTimeUnixNano, span.EndTimeUnixNano))
						}
					}
				}

				return t, nil
			})
		},
	}

	cmd.Flags().BoolVar(&available, "available", false, availableFlagHelp)

	return cmd
}

func newTracesTagsCommand(f *Factory) *cobra.Command {
	var available bool

	cmd := &cobra.Command{
		Use:   "tags",
		Short: "List searchable trace tags",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := tempoProxyGet(cmd, f, available, "/api/search/tags", nil)
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				var resp struct {
					TagNames []string `json:"tagNames"`
				}

				if err := jsonUnmarshal(raw, &resp); err != nil {
					return nil, err
				}

				t := output.NewTable("TAG")
				t.Empty = "No trace tags."

				for _, tag := range resp.TagNames {
					t.Add(tag)
				}

				return t, nil
			})
		},
	}

	cmd.Flags().BoolVar(&available, "available", false, availableFlagHelp)

	return cmd
}

func spanDurationMS(startNS, endNS string) string {
	start, err1 := strconv.ParseInt(startNS, 10, 64)
	end, err2 := strconv.ParseInt(endNS, 10, 64)

	if err1 != nil || err2 != nil || end < start {
		return "-"
	}

	return strconv.FormatFloat(float64(end-start)/1e6, 'f', 2, 64)
}
