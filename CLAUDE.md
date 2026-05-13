# llmgate Project Notes

## Architecture

`llmgate` is a Go `1.26.3` CLI. The public entrypoint is
`cmd/llmgate/main.go`; most behavior lives under `internal/`.

- `internal/core`: managed variable names, setup values, source labels, write
  target types, and diagnostic status aggregation.
- `internal/redact`: shared secret masking and home-path shortening for all
  terminal-visible output.
- `internal/system`: injectable filesystem, environment, command, terminal, and
  platform adapters plus platform path discovery.
- `internal/settings`: Claude and IDE JSONC parsing/upserts with `hujson`.
- `internal/shell`: line-oriented POSIX/fish profile parsing and assignment
  writing.
- `internal/gateway`: LiteLLM/OpenAI-compatible model listing, model probes,
  recommendation, and request-result caching.
- `internal/config`: approved source reads and source precedence resolution.
- `internal/diagnose`: diagnostic engine and renderer.
- `internal/apply`: apply-plan construction, backups, atomic writes, and Windows
  user environment updates.
- `internal/wizard`: no-argument interactive flow built around prompt
  abstractions and `huh`.
- `internal/e2e`: tagged acceptance harness and fake platform/gateway systems.
- `internal/ci` and `scripts`: CI/release and run-script/package verification.

## Commands

- `make fmt`: run `gofmt` over the repository.
- `make test`: run the default unit and fast integration suite.
- `make test-e2e`: run the `e2e` build-tag acceptance suite.
- `make lint`: install and run pinned `golangci-lint v2.12.2` under
  `.tools/bin`.
- `make check`: run formatting, linting, default tests, and e2e tests.
- `make package`: build rolling `main` archives for Linux, macOS, and Windows on
  amd64 and arm64.

## Conventions

- Preserve the startup privacy boundary: before approval, the wizard must not
  read files, stat paths, inspect environment variables, run commands, make HTTP
  calls, or write anything.
- Keep all user-visible errors, diagnostics, apply plans, and test prompt
  records redacted with `internal/redact` behavior.
- Use `config.Read(..., approved)` as the read gate and `diagnose.Run` as the
  aggregation boundary for initial and final diagnostics.
- Shell profile support intentionally handles only active line-based managed
  assignments. Legacy managed blocks are not special.
- JSONC writers should preserve unrelated settings and comments where practical,
  but byte-for-byte formatting preservation is not a requirement.
- E2E tests use the fake filesystem/platform/gateway harness and should avoid
  real local state or external network dependencies.

## Dependencies

Key libraries are `charm.land/huh/v2` for interactive prompts,
`github.com/tailscale/hujson` for JSONC, `golang.org/x/term` for terminal
checks, `golang.org/x/sys` for platform APIs, and `charmbracelet/x/xpty` for
terminal smoke tests.

## Release And Run

GitHub Actions runs CI on Linux, macOS, and Windows. Pushes to `main` publish a
rolling prerelease named `main`; assets are overwritten in place. Run scripts
verify SHA-256 checksums before caching and starting the app.

Useful script checks:

- `scripts/package.sh --dry-run --dist dist`
- `shellcheck scripts/run.sh`
- `pwsh -NoProfile -NonInteractive -Command '$null = [scriptblock]::Create((Get-Content -Raw ./scripts/run.ps1))'`

## Debugging Notes

- Gateway validation first requests `/v1/models`, falls back to `/models` only
  on `404`, and probes selected models through `/v1/chat/completions`.
- Gateway request caches are keyed by normalized URL, token, and model. Retry
  paths bypass cached failures.
- Diagnostics can downgrade failures to warnings when another configuration
  context is usable.
- Apply-plan backups use `.llmgate.bak` first, then timestamped backup paths
  with collision suffixes.
