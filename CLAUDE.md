# lean-cli — repo guide

`leanctl`, the LeanSignal command-line client. A Go/Cobra binary that talks to
**one tenant's lean-api** over the public REST surface, authenticated with an
`lsp_` personal access token. Cross-repo architecture lives in the workspace-level
`../CLAUDE.md`; this file is about working *inside* lean-cli.

> **Repo is `lean-cli`, binary is `leanctl`.** Module path
> `github.com/leansignal/lean-cli`, `cmd/leanctl/main.go` builds `leanctl`.

## Server-side dependency

The design that this CLI implements the client half of lives in
**`../lean-api/docs/leanctl-design.md`**. The relevant part: lean-api's public API
is session-cookie-authenticated today, and both auth layers
(`apimiddleware.RequireSession` and the per-route `guard.*` middleware) read the
cookie directly. Until the server-side `Authenticate` middleware lands, an `lsp_`
token authenticates only the `:8081` MCP listener, not `/api/v1/*`.

Two consequences the CLI already encodes:

- **`/api/v1/tenant-admin/*` and `/api/v1/support/*` can never work with a token.**
  `internal/cc.Client.buildURL` injects a `session_id` that control-center
  validates, and a token has no CC session. There are deliberately no `user` or
  `support` commands; the error path expects `403 session_required`.
- **Scopes narrow, roles decide.** A token carries a role snapshot; the effective
  permission is role ∩ scope. `insufficient_scope` is handled as its own hint in
  `client/errors.go`.

## Commands

`make help` lists them. The ones that matter day to day:

| Task | Command |
|---|---|
| Build | `make build` → `bin/leanctl` (version stamped via ldflags) |
| Test | `make test` (`-race`: `make test-race`) |
| Format / vet / lint | `make fmt` / `make vet` / `make lint` |
| Quality bundle | `make check` (fmt + vet + lint) |
| Full CI | `make ci` |
| Local release build | `make snapshot` (goreleaser, no publish) |
| Coverage | `make cover` → `coverage/coverage.html` |

Release = push a `v*` tag; `.github/workflows/release.yml` runs goreleaser and
cosign-signs the checksums. There is no version file to hand-edit —
`internal/build.Version` is set at link time.

## Layout

```
cmd/leanctl/main.go        os.Exit(cli.Execute())
internal/build/            version stamped by -ldflags
internal/cli/              cobra tree, one file per noun
internal/client/           HTTP client, error envelope, paging, models
internal/config/           config file, contexts, precedence, tenant resolve
internal/output/           table / json / yaml / name renderers
```

### `internal/cli`

`root.go` owns the `Factory` — lazy resolution of settings → client → printer, so
commands that touch no API (`version`, `completion`, `context`) never require a
credential. `NewRootCommand` wires every noun; `helpers.go` holds the generics
(`runList`, `runGet`, `resolveID`) that keep each resource file to its own shape.

- **`resolveID`** lets any positional argument be a name *or* an id: a UUID is
  used as-is, anything else is looked up by exact name in the collection. An
  ambiguous name is a usage error, never a coin flip.
- **`mergeBody`** overlays flag values on a `--file` body, so
  `--file d.json --name other` behaves the way people expect.
- **`query.go`** holds what the three query nouns share: the stored/available
  proxy pair, `parseTime` (RFC3339 / unix / `now-1h`), `formatLabels` (sorted, so
  output diffs cleanly), and `emptyStoredHint`.

### `internal/client`

`Do` returns raw bytes; typed decoding is layered on top. That is deliberate —
`-o json` prints the server's own document, so a field the CLI does not model is
never lost. `models.go` carries only what tables render.

Paging: `client.List[T]` fetches one page, or every page under `--all`, and
re-encodes an accumulated envelope so `--all -o json` is still one document.

### `internal/config`

kubectl-shaped contexts, one per tenant. Viper reads the file and the environment
(prefix `LEANCTL`); `gopkg.in/yaml.v3` writes it, because viper's writer reorders
and re-cases keys. `Resolve` applies flag → env → file precedence.

## Conventions & gotchas

- **No `--token` flag anywhere.** A command line is world-readable via `/proc` and
  lands in shell history. Credentials come from the login prompt, `--token-stdin`,
  or `LEANCTL_TOKEN`. `cli_test.go` asserts the flag's absence across the whole
  tree — do not add it.
- **Config permissions are enforced, not suggested.** `config.Load` refuses a file
  with any group/other bits set, because it holds a bearer token. `Save` writes to
  a temp file, chmods `0600`, then renames.
- **Plain HTTP is refused for non-loopback hosts** (`RequireCredential`). There is
  no insecure-skip-verify escape hatch; if one is ever needed it must be a loud,
  explicitly-named flag, never a silent default.
- **stdout is for data, stderr is for talk.** Empty-result messages, warnings, and
  progress go to stderr so `| jq` and `| wc -l` stay honest. `Printer.Message` is
  suppressed entirely for `-o json`/`-o yaml`/`-o name`.
- **Tables are `text/tabwriter`, never box-drawing.** A test asserts this: box
  characters would break `awk`/`cut` pipelines.
- **Exit codes are a contract** (`client/errors.go`): 0 ok, 1 generic, 2 usage,
  3 auth, 4 not found, 5 validation, 6 server, 7 network. Renumbering breaks
  scripts. Global flag validation happens in root's `PersistentPreRunE` so `-o
  toml` exits 2 rather than hitting a credential error first.
- **Destructive commands take `--yes`** and refuse to run on a non-interactive
  stdin without it — a script must never hang on a prompt nobody can answer. A
  test walks the tree to enforce this for every `delete`/`revoke`.
- **`--available` is the product's core idea, not a convenience flag.** Stored =
  central, demanded only, 30d. Available = the agent's local store, everything it
  collects, ~1d metrics / ~1h logs and traces. Every query command that can read
  the agent side exposes it under exactly that name; a test enforces the set.
  When a stored query returns nothing, `emptyStoredHint` must keep pointing at
  both `filter list` and `--available` — an empty central result is far more often
  a demand gap than an outage, and that is the single most confusing thing a new
  user hits.
- **Complex resources take `--file`.** Alert rules, synthetics, channels, and
  dashboards have too many fields for flags alone, so `get -o json` → edit →
  `update -f` is the intended loop. Convenience flags cover the common fields and
  override the file.
- **Proxy paths are exact.** lean-api allow-lists what may reach Loki and Tempo
  (`isAllowedLokiPath`, `isAllowedTempoPath` — GET only, read paths only). Loki's
  `/tail` is excluded because it upgrades to a WebSocket, which is why there is no
  `logs tail` command. Do not add one without a server-side transport for it.
