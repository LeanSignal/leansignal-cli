# leanctl

The LeanSignal command-line client.

`leanctl` authenticates with a personal access token and acts with **your own
identity and role** — what you can do in the web app is what you can do here, and
nothing more.

```console
$ leanctl auth login --api-url https://petkopuma-api.eu11.leansignal.io
Personal access token: ****
Logged in to https://petkopuma-api.eu11.leansignal.io as nikola@example.com (admin)

$ leanctl demand list
NAME           DESCRIPTION              CREATED BY            AGE
host-metrics   node health and disk     nikola@example.com    12d
kubernetes     cluster + workloads      nikola@example.com    5d

$ leanctl demand export host-metrics > demands/host-metrics.json
```

> **Server support required.** The lean-api change that lets a personal access
> token authenticate the REST API is described in
> [`lean-api/docs/leanctl-design.md`](https://github.com/LeanSignal/lean-api/blob/main/docs/leanctl-design.md).
> Until it ships, the token only authenticates the MCP listener.

## Install

Download a binary from [Releases](https://github.com/LeanSignal/lean-cli/releases),
or build from source:

```bash
git clone git@github.com:LeanSignal/lean-cli.git
cd lean-cli && make install
```

Shell completion:

```bash
leanctl completion zsh > "${fpath[1]}/_leanctl"
leanctl completion bash > /etc/bash_completion.d/leanctl
```

## Authenticate

1. In the web app, go to **Preferences → Access tokens** and mint a token.
   Scopes are `read` (always granted), `write` (create and update), and
   `write:delete` (delete; implies `write`). Write scopes need the editor or
   admin role.
2. `leanctl auth login --api-url https://<tenant>-api.<region>.leansignal.io`
   and paste it. That URL is the one the web app already talks to; you supply it
   once, and it is stored in the context.

The token is verified against `/auth/me` before anything is written to disk, and
stored at `~/.config/leanctl/config.yaml` with mode `0600`.

`leanctl` never contacts control-center. It talks to one tenant's API and
nothing else, so there is no tenant-slug lookup and no second host in the trust
path — which is why the endpoint is given rather than resolved.

In CI, skip login entirely:

```bash
export LEANCTL_TOKEN=lsp_…
export LEANCTL_API_URL=https://<tenant>-api.eu11.leansignal.io
leanctl demand import --file demands/host-metrics.json --dry-run
```

### Multiple tenants

Contexts work the way kubectl's do:

```bash
leanctl auth login --api-url https://petkopuma-api.eu11.leansignal.io   # saves a context
leanctl auth login --api-url https://lean-api.eu11.leansignal.io        # and another
leanctl context list
leanctl context use lean
leanctl demand list --context petkopuma    # or override per command
```

The context is named after the tenant slug recovered from the host; pass
`--name` to call it something else.

## Stored vs Available

LeanSignal stores only **demanded** telemetry. Every query command therefore has
two sides:

|  | default | `--available` |
|---|---|---|
| Reads | the central store | the connected agent's local store |
| Holds | only demanded data | everything the agent collects |
| Retention | 30 days | ~1d metrics, ~1h logs and traces |
| Answers | "what are we keeping?" | "what *could* we demand?" |

An empty result from the central store is usually a demand gap, not an outage,
so `leanctl` says so and hands you the next command:

```console
$ leanctl logs query '{job="nginx"}'
No stored logs matched.

The central store holds only demanded telemetry, so this may be a demand gap
rather than an absence of data. Check both sides:
  leanctl filter list --type log
  leanctl logs query '{job="nginx"}' --available
```

## Commands

| Command | Does |
|---|---|
| `auth` | `login`, `logout`, `status`, `tokens list\|create\|revoke` |
| `context` | `list`, `use`, `delete`, `current` |
| `demand` | `list`, `get`, `create`, `update`, `delete`, `export`, `import` |
| `dashboard` | `list`, `get`, `apply`, `delete`, `versions` |
| `agent` | `list`, `get`, `create`, `update`, `delete`, `diagnose` |
| `alert` | `list`, `get`, `create`, `update`, `delete`, `pause`, `resume`, `mute`, `unmute`, `test` |
| `channel` | `list`, `get`, `create`, `update`, `delete`, `test` |
| `synthetic` | `list`, `get`, `create`, `update`, `delete`, `pause`, `resume`, `test`, `results` |
| `rule` | custom rules: `list`, `get`, `create`, `update`, `delete`, `enable`, `disable` |
| `filter` | demand set: `list`, `purged`, `sync`, `sweep` |
| `metrics` | `names`, `query`, `query-range`, `series`, `labels`, `label-values` |
| `logs` | `query`, `labels`, `label-values`, `stats` |
| `traces` | `search`, `get`, `tags` |
| `status` | what this tenant is storing |
| `audit` | `list` (admin) |
| `settings` | `get`, `set` (admin) |
| `search` | find anything by name |

**User management, invitations, and support cases are not here, by design.**
Those live in control-center and need a real browser session, which a token does
not carry — so `leanctl` has no commands for them rather than commands that
cannot work. Use the web app.

Run `leanctl <command> --help` for flags and examples.

## Output

```bash
leanctl demand list                  # aligned plain text, headers on
leanctl demand list -o json | jq .   # the server's own JSON, unreshaped
leanctl demand list -o yaml
leanctl demand list -o wide          # extra columns, including ids
leanctl demand list -o name | xargs  # bare ids, for scripting
leanctl demand list --no-headers
```

Tables are space-padded text — never box-drawing — so `awk` and `cut` keep
working. Empty results write their explanation to **stderr**, leaving stdout
empty for pipelines. Colour turns itself off when stdout is not a terminal or
`NO_COLOR` is set.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | generic failure |
| 2 | usage error (bad flags or arguments) |
| 3 | authentication or authorization (401/403, or no credential configured) |
| 4 | not found (404) |
| 5 | validation (422) |
| 6 | server error (5xx) |
| 7 | the API could not be reached |

## Demands in git

Export and import round-trip a whole demand — dashboards and alert rules
included, UUIDs stripped, channels referenced by name — which makes a demand a
reviewable artifact:

```bash
leanctl demand export host-metrics > demands/host-metrics.json   # commit it
leanctl demand import --file demands/host-metrics.json --dry-run # PR check
leanctl demand import --file demands/host-metrics.json           # on merge
```

`--dry-run` validates and reports without writing. Missing notification channels
are reported as warnings, not errors, and any rule left without a channel is
imported paused.

## Configuration

Precedence: **flags → environment → config file**.

| Variable | Effect |
|---|---|
| `LEANCTL_TOKEN` | personal access token |
| `LEANCTL_API_URL` | tenant API origin |
| `LEANCTL_CONTEXT` | context to use |
| `LEANCTL_OUTPUT` | default output format |
| `LEANCTL_CONFIG` | config file path |
| `NO_COLOR` | disable colour |

## Security

- The token travels in an `Authorization` header, **never** in a URL.
- **There is no `--token` flag.** A command line is visible to every process on
  the host and lands in shell history; use the login prompt, `--token-stdin`, or
  `LEANCTL_TOKEN`.
- The config file is written `0600` inside a `0700` directory, atomically.
  `leanctl` **refuses to read** a config file others can read.
- Sending a token over plain HTTP to a non-loopback host is refused outright.
- TLS 1.2 is the floor. There is no flag to skip certificate verification.
- `leanctl auth logout --revoke` revokes the token server-side as well as
  removing it locally.
- Exactly one host is ever contacted: the tenant API you configured. No
  telemetry, no update check, no control-center call.
- Destructive commands prompt, and refuse to run unattended without `--yes`.

## Development

```bash
make build     # ./bin/leanctl
make test      # go test ./...
make check     # fmt + vet + lint
make ci        # everything CI runs
make snapshot  # build release artifacts locally
```

Releases are cut by pushing a `v*` tag; goreleaser builds the matrix and cosign
signs the checksums.
