# llmgate

`llmgate` is a public Go CLI wizard for configuring Claude Code to use a
LiteLLM-compatible gateway.

The no-argument command will guide a user through inspecting existing Claude
Code configuration, validating gateway credentials, selecting models, previewing
changes, writing selected targets, and rerunning diagnostics. The implementation
is in progress; builds from `main` should be treated as rolling prereleases.

## Install

Install scripts will be published with the rolling `main` release.

Unix placeholder:

```sh
curl -fsSL https://github.com/r13v/llmgate/releases/download/main/install.sh | sh
```

PowerShell placeholder:

```powershell
iwr https://github.com/r13v/llmgate/releases/download/main/install.ps1 -UseB | iex
```

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

## Development

The project uses Go `1.26.3`.

The command supports:

- `llmgate --help`
- `llmgate --version`
- `llmgate` for the interactive setup flow
