package cli

import (
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/leansignal/lean-cli/internal/client"
	"github.com/leansignal/lean-cli/internal/output"
)

func newMetricsCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "metrics",
		Aliases: []string{"metric"},
		Short:   "Query metrics",
		Long: `Query metrics on either side of the demand line.

By default these commands read the central store, which holds only demanded
series with 30 days of retention. Pass --available to read the connected agent's
local store instead, which holds everything it collects for about a day —
that is how you find out whether a metric could be demanded.`,
	}

	cmd.AddCommand(
		newMetricsNamesCommand(f),
		newMetricsQueryCommand(f),
		newMetricsQueryRangeCommand(f),
		newMetricsSeriesCommand(f),
		newMetricsLabelsCommand(f),
		newMetricsLabelValuesCommand(f),
	)

	return cmd
}

// promProxyGet issues a GET against the metrics proxy pair.
func promProxyGet(
	cmd *cobra.Command, f *Factory, available bool, suffix string, q url.Values,
) ([]byte, error) {
	ctx, cancel := cmdContext(cmd)
	defer cancel()

	api, err := f.Client()
	if err != nil {
		return nil, err
	}

	return api.Get(ctx, metricsProxy.path(available, suffix), q)
}

func newMetricsNamesCommand(f *Factory) *cobra.Command {
	var available bool

	cmd := &cobra.Command{
		Use:     "names",
		Aliases: []string{"list", "ls"},
		Short:   "List metric names",
		Example: `  leanctl metrics names
  leanctl metrics names --available   # what the agent collects but may not store`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := promProxyGet(cmd, f, available, "/api/v1/label/__name__/values", nil)
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

				t := output.NewTable("NAME")
				if !available {
					t.Empty = emptyStoredHint(metricSignal, "")
				} else {
					t.Empty = "The agent reports no metrics. Is it connected?"
				}

				for _, name := range resp.Data {
					t.Add(name)
				}

				return t, nil
			})
		},
	}

	cmd.Flags().BoolVar(&available, "available", false, availableFlagHelp)

	return cmd
}

func newMetricsQueryCommand(f *Factory) *cobra.Command {
	var (
		available bool
		at        string
	)

	cmd := &cobra.Command{
		Use:     "query <promql>",
		Short:   "Run an instant PromQL query",
		Example: `  leanctl metrics query 'up'`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{"query": {args[0]}}

			if at != "" {
				ts, err := parseTime(at)
				if err != nil {
					return err
				}

				q.Set("time", ts)
			}

			raw, err := promProxyGet(cmd, f, available, "/api/v1/query", q)
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				var resp promResponse
				if err := jsonUnmarshal(raw, &resp); err != nil {
					return nil, err
				}

				t := output.NewTable("VALUE", "TIMESTAMP", "SERIES")
				if !available {
					t.Empty = emptyStoredHint(metricSignal, args[0])
				} else {
					t.Empty = "No series matched at the agent."
				}

				for _, r := range resp.Data.Result {
					ts, val := promSample(r.Value)
					t.Add(val, ts, formatLabels(r.Metric))
				}

				return t, nil
			})
		},
	}

	cmd.Flags().BoolVar(&available, "available", false, availableFlagHelp)
	cmd.Flags().StringVar(&at, "at", "", "evaluate at this time (RFC3339 or now-5m)")

	return cmd
}

func newMetricsQueryRangeCommand(f *Factory) *cobra.Command {
	var (
		available bool
		tr        timeRangeFlags
	)

	cmd := &cobra.Command{
		Use:     "query-range <promql>",
		Short:   "Run a range PromQL query",
		Example: `  leanctl metrics query-range 'rate(http_requests_total[5m])' --start now-6h --step 5m`,
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
				"start": {start},
				"end":   {end},
				"step":  {orElse(tr.step, "60s")},
			}

			raw, err := promProxyGet(cmd, f, available, "/api/v1/query_range", q)
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				var resp promResponse
				if err := jsonUnmarshal(raw, &resp); err != nil {
					return nil, err
				}

				t := output.NewTable("TIMESTAMP", "VALUE", "SERIES")
				if !available {
					t.Empty = emptyStoredHint(metricSignal, args[0])
				} else {
					t.Empty = "No series matched at the agent."
				}

				limit := tr.limit
				if limit <= 0 {
					limit = 200
				}

				rows := 0

				for _, r := range resp.Data.Result {
					labels := formatLabels(r.Metric)

					for _, pair := range r.Values {
						if rows >= limit {
							t.Add("…", "…", "output truncated; raise --limit or use -o json")

							return t, nil
						}

						ts, val := promSample(pair)
						t.Add(ts, val, labels)

						rows++
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
	fs.StringVar(&tr.step, "step", "60s", "resolution step")
	fs.IntVar(&tr.limit, "limit", 200, "maximum rows to print in table output")

	return cmd
}

func newMetricsSeriesCommand(f *Factory) *cobra.Command {
	var (
		available bool
		start     string
		end       string
	)

	cmd := &cobra.Command{
		Use:     "series <matcher>",
		Short:   "List series matching a selector",
		Example: `  leanctl metrics series '{__name__="up"}'`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{"match[]": {args[0]}}

			for name, val := range map[string]string{"start": start, "end": end} {
				if val == "" {
					continue
				}

				ts, err := parseTime(val)
				if err != nil {
					return err
				}

				q.Set(name, ts)
			}

			raw, err := promProxyGet(cmd, f, available, "/api/v1/series", q)
			if err != nil {
				return err
			}

			p, err := f.Printer()
			if err != nil {
				return err
			}

			return p.Emit(raw, func() (*output.Table, error) {
				var resp struct {
					Data []map[string]string `json:"data"`
				}

				if err := jsonUnmarshal(raw, &resp); err != nil {
					return nil, err
				}

				t := output.NewTable("SERIES")
				if !available {
					t.Empty = emptyStoredHint(metricSignal, args[0])
				} else {
					t.Empty = "No series matched at the agent."
				}

				for _, s := range resp.Data {
					t.Add(formatLabels(s))
				}

				return t, nil
			})
		},
	}

	cmd.Flags().BoolVar(&available, "available", false, availableFlagHelp)
	cmd.Flags().StringVar(&start, "start", "", "restrict to series present after this time")
	cmd.Flags().StringVar(&end, "end", "", "restrict to series present before this time")

	return cmd
}

func newMetricsLabelsCommand(f *Factory) *cobra.Command {
	var available bool

	cmd := &cobra.Command{
		Use:   "labels",
		Short: "List label names",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := promProxyGet(cmd, f, available, "/api/v1/labels", nil)
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
				t.Empty = "No labels."

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

func newMetricsLabelValuesCommand(f *Factory) *cobra.Command {
	var available bool

	cmd := &cobra.Command{
		Use:     "label-values <label>",
		Short:   "List the values of a label",
		Example: `  leanctl metrics label-values instance`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			label := url.PathEscape(args[0])
			if label == "" {
				return client.Usage("a label name is required")
			}

			raw, err := promProxyGet(cmd, f, available, "/api/v1/label/"+label+"/values", nil)
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

func orElse(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
}
