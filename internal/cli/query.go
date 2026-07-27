package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/leansignal/lean-cli/internal/client"
)

// The query commands (metrics, logs, traces) all speak to a pair of proxies:
// the "stored" one in front of the central dataplane, and the "available" one
// tunneled to the connected agent's local store. Which pair is used is the only
// difference between them, so the plumbing lives here.

type signalProxy struct {
	stored    string
	available string
}

var (
	metricsProxy = signalProxy{stored: "/metrics/dpvm_proxy", available: "/metrics/avm_proxy"}
	logsProxy    = signalProxy{stored: "/logs/dploki_proxy", available: "/logs/aloki_proxy"}
	tracesProxy  = signalProxy{stored: "/traces/dptempo_proxy", available: "/traces/atempo_proxy"}
)

// path returns the proxy prefix for the requested side.
func (s signalProxy) path(available bool, suffix string) string {
	base := s.stored
	if available {
		base = s.available
	}

	return base + suffix
}

// storedVsAvailable is the shared flag description, so the distinction reads
// the same on every command that offers it.
const availableFlagHelp = "query the agent's local store (everything it collects) " +
	"instead of the central store (demanded only)"

// signalKind names the two forms a query command needs: the noun the user types
// (`leanctl logs …`) and the singular the filter list is keyed by (`--type log`).
type signalKind struct {
	noun       string // logs, metrics, traces
	filterType string // log, metric, trace
	verb       string // the subcommand that takes an expression
}

var (
	metricSignal = signalKind{noun: "metrics", filterType: "metric", verb: "query"}
	logSignal    = signalKind{noun: "logs", filterType: "log", verb: "query"}
	traceSignal  = signalKind{noun: "traces", filterType: "trace", verb: "search"}
)

// emptyStoredHint explains an empty central result, which is far more often a
// demand gap than an outage. Printed to stderr so piped output stays clean.
//
// The suggested commands must be copy-pasteable: the expression is wrapped in
// single quotes because selectors contain double quotes, and %q would escape
// them into something the shell rejects.
func emptyStoredHint(s signalKind, expression string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "\nNo stored %s matched.\n\n", s.noun)
	b.WriteString("The central store holds only demanded telemetry, so this may be a demand gap\n")
	b.WriteString("rather than an absence of data. Check both sides:\n")
	fmt.Fprintf(&b, "  leanctl filter list --type %s\n", s.filterType)

	if expression != "" {
		fmt.Fprintf(&b, "  leanctl %s %s %s --available\n", s.noun, s.verb, shellQuote(expression))
	} else {
		fmt.Fprintf(&b, "  leanctl %s %s --available\n", s.noun, s.verb)
	}

	return b.String()
}

// shellQuote wraps a value in single quotes for copy-paste into a shell,
// escaping any single quote it already contains.
func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// parseTime accepts RFC3339, a Unix timestamp, or a relative form like
// now-1h / now-30m, matching what the API and the MCP tools already accept.
func parseTime(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}

	if v == "now" {
		return strconv.FormatInt(time.Now().Unix(), 10), nil
	}

	if rest, ok := strings.CutPrefix(v, "now-"); ok {
		d, err := time.ParseDuration(rest)
		if err != nil {
			return "", client.Usage("invalid relative time %q: %v", v, err)
		}

		return strconv.FormatInt(time.Now().Add(-d).Unix(), 10), nil
	}

	if rest, ok := strings.CutPrefix(v, "now+"); ok {
		d, err := time.ParseDuration(rest)
		if err != nil {
			return "", client.Usage("invalid relative time %q: %v", v, err)
		}

		return strconv.FormatInt(time.Now().Add(d).Unix(), 10), nil
	}

	if ts, err := strconv.ParseInt(v, 10, 64); err == nil {
		return strconv.FormatInt(ts, 10), nil
	}

	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return "", client.Usage(
			"invalid time %q: use RFC3339 (2026-07-27T10:00:00Z), a Unix timestamp, or now-1h", v)
	}

	return strconv.FormatInt(t.Unix(), 10), nil
}

// timeRangeFlags are the --start/--end/--step trio shared by range queries.
type timeRangeFlags struct {
	start string
	end   string
	step  string
	limit int
}

// formatLabels renders a label set deterministically, so two runs of the same
// query produce diffable output.
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}

	return "{" + strings.Join(parts, ", ") + "}"
}

// promResponse is the shape both VictoriaMetrics proxies return.
type promResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
			Values [][]any           `json:"values"`
		} `json:"result"`
	} `json:"data"`
	// Instant label/series queries return a bare array instead.
	ArrayData []string `json:"-"`
	Error     string   `json:"error"`
}

// promSample renders one [timestamp, value] pair.
func promSample(pair []any) (string, string) {
	if len(pair) < 2 {
		return "-", "-"
	}

	ts := "-"

	switch v := pair[0].(type) {
	case float64:
		ts = time.Unix(int64(v), 0).UTC().Format(time.RFC3339)
	case string:
		ts = v
	}

	val := "-"
	if s, ok := pair[1].(string); ok {
		val = s
	}

	return ts, val
}
