package cli

import (
	"github.com/spf13/cobra"

	"github.com/leansignal/leansignal-cli/internal/client"
	"github.com/leansignal/leansignal-cli/internal/output"
)

func newStatusCommand(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show what this tenant is storing",
		Long: `Report the central stores' health and how much each signal holds.

Series counts above the number of currently-active series are expected: a
selector keeps matching data from workloads that have since gone away, and that
history ages out through retention rather than being evicted.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGet(cmd, f, "/leansignal_stack_status", nil,
				func(s client.StackStatus, _ bool) *output.Table {
					t := output.NewTable("SIGNAL", "REACHABLE", "STORED", "DETAIL")

					t.Add("metrics", s.DataplaneVM.Reachable,
						output.Bytes(s.DataplaneVM.DataSizeBytes),
						formatCount(s.DataplaneVM.SeriesCount)+" series")

					logDetail := "-"
					if s.Logs.WindowDays > 0 {
						logDetail = formatCount(int64(s.Logs.WindowDays)) + "d window"
					}

					t.Add("logs", s.Logs.Reachable, output.Bytes(s.Logs.VolumeBytes), logDetail)
					t.Add("traces", s.Traces.Reachable, "-", formatCount(s.Traces.SpanCount)+" spans")

					if s.DataplaneVM.TotalDiskBytes > 0 {
						t.Add("disk", true,
							output.Bytes(s.DataplaneVM.TotalDiskBytes-s.DataplaneVM.FreeDiskBytes),
							"of "+output.Bytes(s.DataplaneVM.TotalDiskBytes)+" used")
					}

					for _, e := range []struct{ signal, msg string }{
						{"metrics", s.DataplaneVM.Error},
						{"logs", s.Logs.Error},
						{"traces", s.Traces.Error},
					} {
						if e.msg != "" {
							t.Add(e.signal+" error", false, "-", e.msg)
						}
					}

					return t
				})
		},
	}

	return cmd
}

// formatCount adds thousands separators, so a seven-digit series count is
// readable at a glance.
func formatCount(n int64) string {
	s := output.Cell(n)
	if len(s) <= 3 {
		return s
	}

	var (
		out   []byte
		start = 0
	)

	if s[0] == '-' {
		out = append(out, '-')
		start = 1
	}

	digits := s[start:]
	for i, d := range []byte(digits) {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out = append(out, ',')
		}

		out = append(out, d)
	}

	return string(out)
}
