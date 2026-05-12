# llmgate

`llmgate` is a public Go CLI wizard for configuring Claude Code to use a
LiteLLM-compatible gateway.

The no-argument command inspects existing Claude Code configuration after user
approval, validates gateway credentials, recommends or selects Claude model
mapping, previews an apply plan, writes selected targets, and reruns diagnostics.
Builds from `main` are rolling prereleases and can change on every push.

## Install

Install scripts are published with the rolling `main` prerelease. Assets are
replaced on every push to `main`, so inspect the release notes and rerun the
installer only when you intentionally want the latest build.

Unix:

```sh
curl -fsSL https://github.com/r13v/llmgate/releases/download/main/install.sh | sh
```

The Unix installer downloads the matching Linux or macOS archive, verifies its
SHA-256 digest against `checksums.txt`, and installs `llmgate` into
`/usr/local/bin` when that directory is writable, otherwise `$HOME/.local/bin`.
Supported overrides are:

- `LLMGATE_INSTALL_DIR`
- `LLMGATE_OS` (`linux` or `darwin`)
- `LLMGATE_ARCH` (`amd64` or `arm64`)

PowerShell:

```powershell
iwr https://github.com/r13v/llmgate/releases/download/main/install.ps1 -UseB | iex
```

The PowerShell installer downloads the matching Windows archive, verifies
`checksums.txt`, and installs `llmgate.exe` into
`$env:LOCALAPPDATA\Programs\llmgate\bin`. Set `LLMGATE_ADD_TO_PATH=1` before
running it to add that directory to the User PATH.

The installers do not support SemVer version selection; they only install the
rolling `main` prerelease.

## Usage

Run the wizard in an interactive terminal:

```sh
llmgate
```

The wizard asks for approval before reading local configuration. If approved, it
runs diagnostics, then offers setup, repair, review-details, or exit actions
depending on the current state. Setup prompts for or reuses a gateway token,
prompts for the LiteLLM-compatible base URL, validates `/v1/models`, recommends
Claude model mapping when possible, probes selected models with a tiny `ping`
chat completion, lets you choose write targets, and shows an apply plan before
anything is changed.

Other public flags:

```sh
llmgate --help
llmgate --version
```

No-argument setup requires an interactive terminal. Non-interactive invocation
fails with a clear message and does not start the wizard.

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

## CI

GitHub Actions runs the project on Linux, macOS, and Windows with Go `1.26.3`.
The workflow runs `make fmt`, verifies the formatted diff is clean, runs
`make lint`, `make test`, and `make test-e2e`, and also uses
`golangci/golangci-lint-action@v9` with pinned `golangci-lint v2.12.2`.

Linux runs `shellcheck scripts/install.sh`, and Windows runs a PowerShell
`scripts/install.ps1 -DryRun` smoke check.

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
also attaches `install.sh` and `install.ps1`. Release notes include the commit
SHA used for the rolling build.
