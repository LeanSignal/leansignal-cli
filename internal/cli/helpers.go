package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/leansignal/leansignal-cli/internal/client"
	"github.com/leansignal/leansignal-cli/internal/output"
)

// uuidRE recognises an id, so a positional argument can be either an id or a
// resource name.
var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func asAPIError(err error, target **client.APIError) bool { return errors.As(err, target) }

// cmdContext returns a context cancelled on SIGINT/SIGTERM, so a long query
// stops promptly on Ctrl-C instead of leaving the terminal wedged.
func cmdContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
}

// listFlags are the pagination and ordering flags every collection shares.
type listFlags struct {
	page    int
	perPage int
	search  string
	orderBy string
	order   string
	all     bool
}

func (l *listFlags) bind(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.IntVar(&l.page, "page", 0, "page number (1-based)")
	fs.IntVar(&l.perPage, "per-page", 0, "items per page")
	fs.StringVar(&l.search, "search", "", "filter by name")
	fs.StringVar(&l.orderBy, "order-by", "", "column to sort by")
	fs.StringVar(&l.order, "order", "", "sort direction: asc|desc")
	fs.BoolVar(&l.all, "all", false, "fetch every page")
}

func (l *listFlags) options() client.ListOptions {
	return client.ListOptions{
		Page: l.page, PerPage: l.perPage, Search: l.search,
		OrderBy: l.orderBy, Order: l.order, All: l.all,
	}
}

// runList fetches a collection and renders it.
func runList[T any](
	cmd *cobra.Command, f *Factory, path string, opts client.ListOptions,
	build func(items []T, wide bool) *output.Table,
) error {
	ctx, cancel := cmdContext(cmd)
	defer cancel()

	api, err := f.Client()
	if err != nil {
		return err
	}

	p, err := f.Printer()
	if err != nil {
		return err
	}

	page, raw, err := client.List[T](ctx, api, path, opts)
	if err != nil {
		return err
	}

	return p.Emit(raw, func() (*output.Table, error) {
		return build(page.Items, p.Wide()), nil
	})
}

// runGet fetches one resource and renders it.
func runGet[T any](
	cmd *cobra.Command, f *Factory, path string, q url.Values,
	build func(item T, wide bool) *output.Table,
) error {
	ctx, cancel := cmdContext(cmd)
	defer cancel()

	api, err := f.Client()
	if err != nil {
		return err
	}

	p, err := f.Printer()
	if err != nil {
		return err
	}

	var item T

	raw, err := api.GetInto(ctx, path, q, &item)
	if err != nil {
		return err
	}

	return p.Emit(raw, func() (*output.Table, error) {
		return build(item, p.Wide()), nil
	})
}

// resolveID turns a positional argument into a resource id. An id is used
// as-is; anything else is looked up by exact name in the collection, which is
// what makes `leanctl demand get host-metrics` work.
func resolveID(cmd *cobra.Command, f *Factory, collectionPath, arg string) (string, error) {
	if arg == "" {
		return "", client.Usage("a name or id is required")
	}

	if uuidRE.MatchString(arg) {
		return arg, nil
	}

	ctx, cancel := cmdContext(cmd)
	defer cancel()

	api, err := f.Client()
	if err != nil {
		return "", err
	}

	type named struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	page, _, err := client.List[named](ctx, api, collectionPath, client.ListOptions{Search: arg, All: true})
	if err != nil {
		return "", err
	}

	var matches []named

	for _, item := range page.Items {
		if strings.EqualFold(item.Name, arg) {
			matches = append(matches, item)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0].ID, nil
	case 0:
		return "", &client.APIError{
			Status:  404,
			Code:    "not_found",
			Message: fmt.Sprintf("no %s named %q", strings.Trim(collectionPath, "/"), arg),
		}
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, m.ID)
		}

		return "", client.Usage("%q is ambiguous — %d matches (%s); pass an id",
			arg, len(matches), strings.Join(ids, ", "))
	}
}

// readBody loads a JSON request body from a file, or from stdin when the path
// is "-". It validates that the content parses, so a typo fails locally with a
// clear message instead of as a server-side 400.
func readBody(path string) ([]byte, error) {
	if path == "" {
		return nil, client.Usage("--file is required (use '-' to read stdin)")
	}

	var (
		raw []byte
		err error
	)

	if path == "-" {
		raw, err = io.ReadAll(io.LimitReader(os.Stdin, 32<<20))
	} else {
		raw, err = os.ReadFile(path)
	}

	if err != nil {
		return nil, client.Usage("reading %s: %v", displayPath(path), err)
	}

	if strings.TrimSpace(string(raw)) == "" {
		return nil, client.Usage("%s is empty", displayPath(path))
	}

	if !json.Valid(raw) {
		return nil, client.Usage("%s is not valid JSON", displayPath(path))
	}

	return raw, nil
}

// readRaw loads a file verbatim, for payloads that are not JSON — the agent's
// collector config is YAML, and validating it here would only duplicate (badly)
// the real check the agent performs before writing.
func readRaw(path string) ([]byte, error) {
	if path == "" {
		return nil, client.Usage("--file is required (use '-' to read stdin)")
	}

	var (
		raw []byte
		err error
	)

	if path == "-" {
		raw, err = io.ReadAll(io.LimitReader(os.Stdin, 32<<20))
	} else {
		raw, err = os.ReadFile(path)
	}

	if err != nil {
		return nil, client.Usage("reading %s: %v", displayPath(path), err)
	}

	if strings.TrimSpace(string(raw)) == "" {
		return nil, client.Usage("%s is empty", displayPath(path))
	}

	return raw, nil
}

func displayPath(p string) string {
	if p == "-" {
		return "stdin"
	}

	return p
}

// confirm asks before a destructive action. It refuses to proceed on a
// non-interactive stdin unless --yes was given, so a script never hangs on a
// prompt nobody can answer.
func confirm(cmd *cobra.Command, yes bool, format string, args ...any) error {
	if yes {
		return nil
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return client.Usage("refusing to %s without confirmation; pass --yes",
			fmt.Sprintf(format, args...))
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "%s? [y/N] ", fmt.Sprintf(format, args...))

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	default:
		return errors.New("aborted")
	}
}

// promptSecret reads a secret from the terminal without echoing it.
func promptSecret(prompt string) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", client.Usage("stdin is not a terminal; use --token-stdin or set LEANCTL_TOKEN")
	}

	fmt.Fprint(os.Stderr, prompt)

	secret, err := term.ReadPassword(int(os.Stdin.Fd()))

	fmt.Fprintln(os.Stderr)

	if err != nil {
		return "", fmt.Errorf("reading token: %w", err)
	}

	return strings.TrimSpace(string(secret)), nil
}

// readStdinLine reads one line from stdin, for --token-stdin.
func readStdinLine() (string, error) {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 8<<10))
	if err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}

	return strings.TrimSpace(string(raw)), nil
}

// emitAction renders the response of a mutating call.
func emitAction(f *Factory, raw []byte, message string, args ...any) error {
	p, err := f.Printer()
	if err != nil {
		return err
	}

	if p.Structured() {
		return p.Emit(raw, nil)
	}

	p.Message(message, args...)

	return nil
}

// mustJSON encodes a request body built from CLI flags. The input is always a
// map or struct assembled in this package, so encoding cannot fail.
func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("encoding request body: %v", err))
	}

	return raw
}

// jsonUnmarshal decodes a server response, wrapping the error with context.
func jsonUnmarshal(raw []byte, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	return nil
}

// mergeBody applies flag-provided fields on top of a --file body, so
// `--file d.json --name other` works the way people expect.
func mergeBody(base []byte, overrides map[string]any) ([]byte, error) {
	doc := map[string]any{}

	if len(base) > 0 {
		if err := json.Unmarshal(base, &doc); err != nil {
			return nil, client.Usage("request body must be a JSON object: %v", err)
		}
	}

	for k, v := range overrides {
		doc[k] = v
	}

	if len(doc) == 0 {
		return nil, client.Usage("nothing to send — pass --file or the relevant flags")
	}

	return mustJSON(doc), nil
}

// queryOf builds a url.Values from alternating key/value pairs, skipping empty
// values so optional filters do not become empty query parameters.
func queryOf(pairs ...string) url.Values {
	q := url.Values{}

	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			q.Set(pairs[i], pairs[i+1])
		}
	}

	return q
}
