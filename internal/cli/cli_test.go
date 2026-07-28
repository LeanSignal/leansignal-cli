package cli

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Every command must carry help text and, where it takes arguments, an
// argument validator — a CLI that accepts stray arguments silently is the
// classic source of "why did nothing happen".
func TestCommandTreeIsWellFormed(t *testing.T) {
	root := NewRootCommand()

	var walk func(*cobra.Command)

	walk = func(c *cobra.Command) {
		if c.Short == "" {
			t.Errorf("%q has no short description", c.CommandPath())
		}

		if !c.HasSubCommands() && c.RunE == nil && c.Run == nil {
			t.Errorf("%q is a leaf with nothing to run", c.CommandPath())
		}

		if c.RunE != nil && c.Args == nil && c.Name() != "help" {
			t.Errorf("%q accepts arguments without validating them", c.CommandPath())
		}

		for _, sub := range c.Commands() {
			walk(sub)
		}
	}

	walk(root)
}

// A --token flag would put the credential in the process table and in shell
// history. It must not exist anywhere in the tree.
func TestNoTokenFlagAnywhere(t *testing.T) {
	root := NewRootCommand()

	var walk func(*cobra.Command)

	walk = func(c *cobra.Command) {
		if c.Flags().Lookup("token") != nil {
			t.Errorf("%q exposes a --token flag; use LEANCTL_TOKEN or auth login", c.CommandPath())
		}

		for _, sub := range c.Commands() {
			walk(sub)
		}
	}

	walk(root)
}

// Every example must be runnable as written: the command path it names must
// resolve, and every flag it passes must exist on that command. This is the
// test that would have caught `auth login --tenant` surviving in an example
// after the flag was removed.
func TestExamplesResolveAndUseRealFlags(t *testing.T) {
	root := NewRootCommand()
	flagRe := regexp.MustCompile(`--([a-z][a-z0-9-]+)`)

	var walk func(*cobra.Command)

	walk = func(c *cobra.Command) {
		for _, line := range strings.Split(c.Example, "\n") {
			idx := strings.Index(line, "leanctl ")
			if idx < 0 {
				continue
			}

			invocation := line[idx+len("leanctl "):]

			target, _, err := root.Find(strings.Fields(invocation))
			if err != nil || target == root && !strings.HasPrefix(invocation, "-") {
				// Find falls back to root for unknown paths; an example naming
				// a nonexistent subcommand must fail, not silently pass.
				if err != nil {
					t.Errorf("%q example does not resolve: %q (%v)", c.CommandPath(), line, err)

					continue
				}
			}

			for _, m := range flagRe.FindAllStringSubmatch(invocation, -1) {
				name := m[1]
				if target.Flags().Lookup(name) == nil &&
					target.InheritedFlags().Lookup(name) == nil &&
					target.PersistentFlags().Lookup(name) == nil {
					t.Errorf("%q example uses --%s, which %q does not define: %q",
						c.CommandPath(), name, target.CommandPath(), line)
				}
			}
		}

		for _, sub := range c.Commands() {
			walk(sub)
		}
	}

	walk(root)
}

// Help prose may only name flags that exist somewhere in the tree, so removing
// a flag forces every sentence that mentioned it to be updated with it.
func TestHelpProseNamesOnlyRealFlags(t *testing.T) {
	root := NewRootCommand()
	flagRe := regexp.MustCompile(`--([a-z][a-z0-9-]+)`)

	known := map[string]bool{}

	var collect func(*cobra.Command)

	collect = func(c *cobra.Command) {
		for _, fs := range []*pflag.FlagSet{c.Flags(), c.PersistentFlags()} {
			fs.VisitAll(func(fl *pflag.Flag) { known[fl.Name] = true })
		}

		for _, sub := range c.Commands() {
			collect(sub)
		}
	}

	collect(root)

	var walk func(*cobra.Command)

	walk = func(c *cobra.Command) {
		for _, text := range []string{c.Short, c.Long, c.Example} {
			for _, m := range flagRe.FindAllStringSubmatch(text, -1) {
				if !known[m[1]] {
					t.Errorf("%q help text names --%s, which no command defines", c.CommandPath(), m[1])
				}
			}
		}

		for _, sub := range c.Commands() {
			walk(sub)
		}
	}

	walk(root)
}

// Public examples stay vanilla: no real tenant slugs, hosts, or people.
func TestHelpTextCarriesNoPersonalIdentifiers(t *testing.T) {
	root := NewRootCommand()

	banned := []string{"petkopuma", "nikola", "datomatics", "lsh "}

	var walk func(*cobra.Command)

	walk = func(c *cobra.Command) {
		blob := strings.ToLower(c.Short + " " + c.Long + " " + c.Example)
		for _, b := range banned {
			if strings.Contains(blob, b) {
				t.Errorf("%q help text contains %q", c.CommandPath(), strings.TrimSpace(b))
			}
		}

		for _, sub := range c.Commands() {
			walk(sub)
		}
	}

	walk(root)
}

// leanctl talks to one tenant's lean-api and nothing else. Control-center is
// not a dependency: it has no CLI-reachable surface (tenant-admin and support
// need a browser session), and depending on it for tenant resolution would put
// a second host in the trust path for no gain. This walks the command tree
// looking for any sign one crept back in.
func TestNoControlCenterSurface(t *testing.T) {
	root := NewRootCommand()

	banned := []string{"cc.leansignal.io", "resolve_tenant", "control-center", "control center"}
	bannedCmd := map[string]bool{"user": true, "users": true, "support": true, "invitation": true,
		"invitations": true, "tenant-admin": true}

	var walk func(*cobra.Command)

	walk = func(c *cobra.Command) {
		if bannedCmd[c.Name()] {
			t.Errorf("%q exposes a control-center-backed surface", c.CommandPath())
		}

		// Explaining that something lives in the web app is fine; offering to
		// reach control-center is not. Only flag help text is checked — that is
		// where a re-added --cc-url would surface. FlagUsages is used rather
		// than VisitAll so the test needs no direct pflag dependency.
		usages := strings.ToLower(c.Flags().FlagUsages())

		for _, bad := range banned {
			if strings.Contains(usages, bad) {
				t.Errorf("%q has a flag referencing control-center (%q):\n%s",
					c.CommandPath(), bad, c.Flags().FlagUsages())
			}
		}

		for _, sub := range c.Commands() {
			walk(sub)
		}
	}

	walk(root)
}

// Deleting is irreversible, so every delete-shaped command must offer --yes and
// therefore prompt without it.
func TestDestructiveCommandsHaveAConfirmationFlag(t *testing.T) {
	root := NewRootCommand()

	destructive := map[string]bool{"delete": true, "revoke": true}

	var walk func(*cobra.Command)

	walk = func(c *cobra.Command) {
		if destructive[c.Name()] && c.Flags().Lookup("yes") == nil {
			t.Errorf("%q deletes without a --yes flag", c.CommandPath())
		}

		for _, sub := range c.Commands() {
			walk(sub)
		}
	}

	walk(root)
}

// The Stored/Available split is the product's core idea; every query command
// that can read the agent side must expose it under the same name.
func TestQueryCommandsExposeAvailable(t *testing.T) {
	root := NewRootCommand()

	want := []string{
		"metrics names", "metrics query", "metrics query-range", "metrics series",
		"logs query", "logs labels", "traces search", "traces get",
	}

	for _, path := range want {
		cmd, _, err := root.Find(strings.Split(path, " "))
		if err != nil {
			t.Fatalf("finding %q: %v", path, err)
		}

		if cmd.Flags().Lookup("available") == nil {
			t.Errorf("%q has no --available flag", path)
		}
	}
}

func TestParseTime(t *testing.T) {
	t.Run("relative", func(t *testing.T) {
		got, err := parseTime("now-1h")
		if err != nil {
			t.Fatalf("parseTime: %v", err)
		}

		ts, err := strconv.ParseInt(got, 10, 64)
		if err != nil {
			t.Fatalf("result is not a unix timestamp: %q", got)
		}

		delta := time.Since(time.Unix(ts, 0))
		if delta < 59*time.Minute || delta > 61*time.Minute {
			t.Errorf("now-1h resolved to %v ago", delta)
		}
	})

	t.Run("rfc3339", func(t *testing.T) {
		got, err := parseTime("2026-07-27T10:00:00Z")
		if err != nil {
			t.Fatalf("parseTime: %v", err)
		}

		if got != "1785146400" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("empty passes through", func(t *testing.T) {
		got, err := parseTime("")
		if err != nil || got != "" {
			t.Errorf("got %q, %v", got, err)
		}
	})

	t.Run("garbage is a usage error", func(t *testing.T) {
		if _, err := parseTime("last tuesday"); err == nil {
			t.Error("expected an error")
		}
	})
}

// Label order must be stable so two runs of the same query diff cleanly.
func TestFormatLabelsIsSorted(t *testing.T) {
	got := formatLabels(map[string]string{"z": "1", "a": "2", "m": "3"})
	if got != "{a=2, m=3, z=1}" {
		t.Errorf("got %q", got)
	}

	if formatLabels(nil) != "-" {
		t.Error("an empty label set should render as a dash")
	}
}

// The empty-result hint is the CLI's answer to LeanSignal's most confusing
// behaviour, so it must name both the demand set and the --available escape.
func TestEmptyStoredHintPointsAtBothSides(t *testing.T) {
	hint := emptyStoredHint(logSignal, `{job="nginx"}`)

	for _, want := range []string{"demanded", "filter list", "--available"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint is missing %q:\n%s", want, hint)
		}
	}
}

func TestMergeBodyOverlaysFlagsOnAFile(t *testing.T) {
	merged, err := mergeBody([]byte(`{"name":"from-file","description":"keep"}`),
		map[string]any{"name": "from-flag"})
	if err != nil {
		t.Fatalf("mergeBody: %v", err)
	}

	got := string(merged)
	if !strings.Contains(got, `"name":"from-flag"`) {
		t.Errorf("flag did not win: %s", got)
	}

	if !strings.Contains(got, `"description":"keep"`) {
		t.Errorf("file field was dropped: %s", got)
	}
}

func TestMergeBodyRejectsAnEmptyRequest(t *testing.T) {
	if _, err := mergeBody(nil, map[string]any{}); err == nil {
		t.Error("expected an error when there is nothing to send")
	}
}
