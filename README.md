# llmgate

`llmgate` is a public Go CLI wizard for configuring Claude Code to use a
LiteLLM-compatible gateway.

The no-argument command inspects existing Claude Code configuration after user
approval, validates gateway credentials, recommends or selects Claude model
mapping, previews an apply plan, writes selected targets, and reruns diagnostics.
Builds from `main` are rolling prereleases and can change on every push.

## Run

Run `llmgate` with one copy/paste command. Builds from `main` are rolling
prereleases and can change on every push.

MacOS/Linux:

```sh
curl -fsSL https://github.com/r13v/llmgate/releases/download/main/run.sh | sh
```

Windows:

```powershell
iwr https://github.com/r13v/llmgate/releases/download/main/run.ps1 -UseB | iex
```

## Run Script Details

The run scripts download the matching archive for your OS and CPU, verify its
SHA-256 digest against `checksums.txt`, cache the verified binary, and start
`llmgate`. On later runs, they check for updates and reuse the cache when the
rolling `main` build has not changed. If the update check fails, a previously
verified cached binary can still run.

Cache locations:

- Unix: `${XDG_CACHE_HOME:-$HOME/.cache}/llmgate/main/<os>-<arch>/`
- Windows: `$env:LOCALAPPDATA\llmgate\cache\main\windows-<arch>\`

The scripts forward arguments to `llmgate`. For example:

```sh
curl -fsSL https://github.com/r13v/llmgate/releases/download/main/run.sh | sh -s -- --version
```

```powershell
& ([scriptblock]::Create((iwr https://github.com/r13v/llmgate/releases/download/main/run.ps1 -UseB).Content)) --version
```

The Unix script requires `curl` or `wget`, `tar`, and one SHA-256 tool:
`sha256sum`, `shasum`, or `openssl`. The PowerShell script requires
`Invoke-WebRequest`, `Get-FileHash`, and `Expand-Archive`.

## Gateway Compatibility

The gateway must expose an OpenAI-compatible model list and chat completions
surface. `llmgate` normalizes the base URL by removing query strings and
fragments, then requests `/v1/models`. If that endpoint returns `404`, it tries
`/models` as a fallback. Model-list responses must contain model IDs in
`data[].id`.

For model probes, `llmgate` sends a small `POST /v1/chat/completions` request
with a single user `ping` message and `max_tokens: 1`. Requests include both
`Authorization: Bearer <token>` and `x-litellm-api-key: <token>` headers.

## Privacy and Safety

`llmgate` does not use telemetry and does not write file logs.

Before startup approval, it does not read files, check file existence, inspect
environment variables, run local commands, make HTTP requests, or write
anything. If startup approval is declined or cancelled, it prints
`No files were read or changed.`

Before apply-plan approval, it does not change files or user environment
variables. Apply plans show target names, paths, operations, old and new values,
backup behavior, warnings, and whether sensitive content is involved.

Secrets are masked in terminal output, diagnostics, errors, gateway failure
details, apply plans, write results, and tests. Home paths are shortened to `~`
in display output.

## Supported Platforms

The supported platforms are:

- Linux amd64 and arm64
- macOS amd64 and arm64
- Windows amd64 and arm64

Linux and macOS use shell profile targets for persisted terminal configuration.
Windows uses user-scoped environment variables instead of shell profile files.
Release archives are built for all six targets on every push to `main`.

## Write Targets

The wizard can write:

- Claude Code user settings at `~/.claude/settings.json`
- zsh, bash, or fish shell profile assignments on Linux and macOS
- Windows User environment variables on Windows
- VS Code user settings when the VS Code settings directory already exists
- Cursor user settings when the Cursor settings directory already exists

Project settings under `./.claude/settings.local.json` and
`./.claude/settings.json` are read for diagnostics and validation, but are not
normal setup write targets.

Existing JSONC settings preserve unrelated keys where possible. Existing shell
profiles preserve unrelated content, comments, and dynamic assignments. Existing
files are backed up before replacement as `.llmgate.bak`, or with a timestamped
fallback if that backup path already exists. Unchanged targets are skipped
without rewriting or creating backups.

Setup writes gateway credentials, model mapping, and privacy or traffic defaults
for Claude Code. Repair mode only updates writable stale simple shell model
assignments; it does not add missing shell variables.

## Managed Configuration Values

Required gateway and model values:

| Name | Purpose |
| --- | --- |
| `ANTHROPIC_AUTH_TOKEN` | Gateway API token |
| `ANTHROPIC_BASE_URL` | LiteLLM-compatible gateway base URL |
| `ANTHROPIC_MODEL` | Primary Claude Code model |
| `ANTHROPIC_DEFAULT_HAIKU_MODEL` | Haiku tier model |
| `ANTHROPIC_DEFAULT_SONNET_MODEL` | Sonnet tier model |
| `ANTHROPIC_DEFAULT_OPUS_MODEL` | Opus tier model |

Behavior and privacy defaults written during setup:

| Name | Value | Purpose |
| --- | --- | --- |
| `CLAUDE_CODE_ENABLE_TELEMETRY` | `0` | Disable Claude Code telemetry |
| `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` | `1` | Reduce Claude Code optional network traffic |
| `OTEL_METRICS_EXPORTER` | `otlp` | Keep telemetry exporter explicit |
| `ANTHROPIC_DISABLE_NONESSENTIAL_TRAFFIC` | `1` | Reduce optional Anthropic traffic |
| `DISABLE_PROMPT_CACHING_HAIKU` | `1` | Disable LiteLLM prompt caching for Haiku tier |
| `DISABLE_PROMPT_CACHING_SONNET` | `1` | Disable LiteLLM prompt caching for Sonnet tier |
| `DISABLE_PROMPT_CACHING_OPUS` | `1` | Disable LiteLLM prompt caching for Opus tier |
| `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS` | `1` | Disable Claude Code experimental beta headers |

## Diagnostics

Diagnostics cover:

- Claude Code CLI availability
- Claude Code user configuration
- config source conflicts
- runtime environment drift
- malformed source files
- project overrides
- gateway validation
- selected model availability
- model probes
- IDE configuration and IDE model validation
- project gateway or model validation
- detected write targets

Diagnostic status severity is `OK < SKIP < WARN < FAIL`. A final setup result of
`OK` prints `Configured`, `WARN` or `SKIP` prints `Configured with warnings`, and
`FAIL` prints `Setup incomplete`. Successful and warning results remind you to
restart your terminal and IDE.

In the interactive wizard, the first diagnostic screen shows actionable
findings before raw checks when a problem can be summarized. Gateway
authentication failures, token conflicts across configuration sources, and IDE
token drift are grouped with short `why`, `evidence`, and `fix` lines so
repeated warnings do not hide the next step. Any warning or failure that is not
covered by a finding is still shown, and `Review details` keeps the full
check-level diagnostic evidence, including sanitized gateway response details
where useful.

## Build and Test

The project uses Go `1.26.3`.

```sh
make build
make test
make test-e2e
make lint
make check
make package
```

`make lint` installs the pinned `golangci-lint v2.12.2` binary under
`.tools/bin`. `make check` runs formatting, linting, default tests, and the
e2e-tagged acceptance suite. `make package` builds the rolling release archives
for Linux, macOS, and Windows on amd64 and arm64.

Packaging can be exercised directly:

```sh
scripts/package.sh --dry-run --dist dist
```

`scripts/package.sh` requires Bash, Go, `tar`, and `zip`. Supported environment
overrides are `GO`, `VERSION`, `COMMIT`, `DATE`, `DIST_DIR`, and
`PACKAGE_PREFIX`.

## CI

GitHub Actions runs the project on Linux, macOS, and Windows with Go `1.26.3`.
Linux runs formatting and linting, verifies the formatted diff is clean, and
uses `golangci/golangci-lint-action@v9` with pinned `golangci-lint v2.12.2`.
All operating systems run `go test ./...` and run `go test -tags=e2e ./...`.

Linux runs `shellcheck scripts/run.sh`, and Windows parses `scripts/run.ps1`
with PowerShell.

## Rolling Main Release

Every push to `main` runs `make check`, builds `llmgate` for Linux, macOS, and
Windows on amd64 and arm64, and publishes a rolling prerelease named `main`.
These assets are overwritten in place and should be treated as prerelease
artifacts, not stable versioned releases.

Release archives:

- `llmgate-main-linux-amd64.tar.gz`
- `llmgate-main-linux-arm64.tar.gz`
- `llmgate-main-darwin-amd64.tar.gz`
- `llmgate-main-darwin-arm64.tar.gz`
- `llmgate-main-windows-amd64.zip`
- `llmgate-main-windows-arm64.zip`

Each archive contains the `llmgate` binary, `README.md`, and `LICENSE`.
`checksums.txt` contains SHA-256 digests for all archives. The rolling release
also attaches `run.sh` and `run.ps1`. Release notes include the commit
SHA used for the rolling build.
