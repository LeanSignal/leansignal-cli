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

## Install

### One-liner (macOS and Linux)

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/lean-cli/main/scripts/install.sh | sh
```

Detects your OS and architecture, downloads the matching release, verifies its
checksum, and installs to `/usr/local/bin` — or `~/.local/bin` when that is not
writable, so it never demands `sudo`. Options: `--version vX.Y.Z`,
`--bin-dir DIR`, `--no-verify`. Review the script before piping it to a shell.

### Manual download

Releases carry `leanctl_<version>_<os>_<arch>.tar.gz` for `darwin` and `linux`,
`amd64` and `arm64`.

```bash
# macOS (Apple Silicon: arm64; Intel: amd64) — check with `uname -m`
curl -fsSLO https://github.com/LeanSignal/lean-cli/releases/latest/download/leanctl_0.8.0_darwin_arm64.tar.gz

# Linux
curl -fsSLO https://github.com/LeanSignal/lean-cli/releases/latest/download/leanctl_0.8.0_linux_amd64.tar.gz

tar -xzf leanctl_*.tar.gz
sudo install -m 0755 leanctl /usr/local/bin/leanctl
leanctl version
```

Without `sudo`, put it anywhere on your `PATH` instead:
`install -m 0755 leanctl ~/.local/bin/leanctl`.

**macOS:** the binaries are signed but not notarized, so a build fetched through
a browser is quarantined and Gatekeeper will refuse it. `curl` does not set that
flag; if you did download it another way, clear it with
`xattr -d com.apple.quarantine leanctl`.

**Verifying the download** (optional). Checksums are signed with cosign
keylessly, so no key is needed:

```bash
base=https://github.com/LeanSignal/lean-cli/releases/latest/download
curl -fsSLO $base/checksums.txt -O $base/checksums.txt.sig -O $base/checksums.txt.pem

shasum -a 256 -c checksums.txt --ignore-missing   # `sha256sum -c` on Linux

cosign verify-blob checksums.txt \
  --signature checksums.txt.sig --certificate checksums.txt.pem \
  --certificate-identity-regexp 'https://github.com/LeanSignal/lean-cli/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

### From source

Needs Go 1.25+.

```bash
git clone https://github.com/LeanSignal/lean-cli.git
cd lean-cli
make install          # -> $GOBIN, or $(go env GOPATH)/bin
leanctl version
```

`make install` does not touch your `PATH`. If `leanctl: command not found`
follows, add Go's bin directory:

```bash
echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.zshrc   # ~/.bashrc on Linux
```

`make build` instead drops the binary in `./bin/leanctl` without installing.

### Shell completion

```bash
# zsh (macOS default)
leanctl completion zsh > "${fpath[1]}/_leanctl"

# bash — Linux
leanctl completion bash | sudo tee /etc/bash_completion.d/leanctl >/dev/null

# bash — macOS with Homebrew's bash-completion@2
leanctl completion bash > "$(brew --prefix)/etc/bash_completion.d/leanctl"

# fish
leanctl completion fish > ~/.config/fish/completions/leanctl.fish
```

Start a new shell afterwards.

### Upgrading and removing

Repeat the install; it overwrites in place. To remove, delete the binary, the
completion file, and `~/.config/leanctl/` (which holds your token — revoke it
first with `leanctl auth logout --revoke`).

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
| `agent` | `list`, `get`, `create`, `update`, `delete`, `diagnose`, `config get\|apply` |
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

## Editing an agent's collector config

`agent config` reaches the collector configuration on the agent host itself,
over the agent's control stream — admin role, connected agent:

```bash
leanctl agent config get lsh                                     # what sources exist
leanctl agent config get lsh --path /etc/leansignal-agent/config.yaml > c.yaml
$EDITOR c.yaml
leanctl agent config apply lsh --file c.yaml
```

The **agent** validates the write — YAML parse plus a full collector dry-run of
the merged config — and applies it only if it passes: previous contents kept as
`<path>.bak`, atomic write, then reload. A rejected config changes nothing on the
host and exits 5 with the validator's own complaint. Values stay unresolved, so
`${env:...}` references are never expanded into the output.

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
