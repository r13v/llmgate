# llmgate

`llmgate` is a public Go CLI wizard for configuring Claude Code to use a
LiteLLM-compatible gateway.

The no-argument command will guide a user through inspecting existing Claude
Code configuration, validating gateway credentials, selecting models, previewing
changes, writing selected targets, and rerunning diagnostics. The implementation
is in progress; builds from `main` should be treated as rolling prereleases.

## Install

Install scripts are published with the rolling `main` prerelease. Assets are
replaced on every push to `main`, so inspect the release notes and rerun the
installer when you intentionally want the latest build.

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

## Build and Test

```sh
make build
make test
make test-e2e
make lint
make check
```

`make lint` installs the pinned `golangci-lint v2.12.2` binary under
`.tools/bin`.

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

Release archives are replaced in place:

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

## Development

The project uses Go `1.26.3`.

The command supports:

- `llmgate --help`
- `llmgate --version`
- `llmgate` for the interactive setup flow
