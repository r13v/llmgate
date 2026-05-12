# llmgate Full Implementation

## Overview
- Implement `llmgate` as a public Go CLI wizard that configures Claude Code to use a LiteLLM-compatible gateway, following `docs/PROJECT_SPEC.md`.
- Build the full no-argument interactive setup flow with `huh`, diagnostics, gateway validation, model selection, apply plans, backups, writes, and final diagnostics.
- Support macOS, Linux, and Windows as first-class platforms.
- Add reproducible tooling, linting, unit tests, fast integration tests, full e2e acceptance tests, GitHub Actions CI, rolling `main` releases, and install scripts.

## Context
- Files/components involved:
  - Existing: `docs/PROJECT_SPEC.md`, `go.mod`, `go.sum`
  - New production entrypoint: `cmd/llmgate/main.go`
  - New internal packages under `internal/...`
  - New tests under matching package directories plus e2e-focused test helpers
  - New tooling files: `Makefile`, `.golangci.yml`, `.gitignore`
  - New CI/release workflows under `.github/workflows/`
  - New install scripts under `scripts/`
  - New docs: `README.md`, `LICENSE`
- Related patterns found:
  - The repo is new, so local implementation patterns do not exist yet.
  - `huh` v2 tests mostly use accessible scripted input via `WithAccessible(true)` and `WithInput(strings.NewReader(...))`.
  - `huh` v2 uses `github.com/charmbracelet/x/xpty` for real pseudo-terminal smoke tests.
- Dependencies identified:
  - Module path: `github.com/r13v/llmgate`
  - Go: fixed `1.26.3`
  - `huh`: `charm.land/huh/v2 v2.0.3`
  - JSONC parser/writer: `github.com/tailscale/hujson`
  - Terminal detection: `golang.org/x/term`
  - Platform APIs: `golang.org/x/sys`
  - Linter: `golangci-lint v2.12.2`
  - GitHub Actions: floating major tags, for example `actions/checkout@v6`

## Scope Decisions
- Implement the full wizard now, not just a scaffold.
- Add `--help` and `--version`; the no-argument wizard remains the main public product flow.
- Use latest `huh` through its current Go module path, `charm.land/huh/v2`.
- Fixed Go and Go dependency versions; GitHub Actions use major tags.
- Network checks may run automatically after startup disclosure approval.
- Write targets default to all detected writable targets, including IDE targets when their settings directories already exist.
- Windows support is required in the first implementation.
- JSONC comments should be preserved where practical, but byte-for-byte formatting preservation is not required.
- Legacy managed shell blocks are fully out of scope and removed from the product spec.
- No telemetry and no file logs.
- Final aggregate `FAIL` exits non-zero.
- Release on every push to `main` as a rolling `main` prerelease.
- Install scripts support only the rolling `main` release and verify checksums.

## Development Approach
- Testing approach: code-first with tests in each task; add e2e coverage as soon as the flow shape exists.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- Every code-change task must include new or updated tests.
- All tests for a task must pass before starting the next task.
- Update this plan when scope changes during implementation.

## Testing Strategy
- Unit tests required for every code-change task.
- Fast integration tests required for config reads/writes, gateway behavior, diagnostics, and apply plan idempotency.
- E2E tests required for wizard flows using:
  - scripted accessible input for deterministic flow coverage;
  - `xpty` smoke tests for the real terminal path;
  - fake filesystem/platform/env/command runner/gateway for acceptance matrix coverage.
- Cover success, error, cancellation, privacy/redaction, and cross-platform path cases.
- Split default tests from the full e2e acceptance suite:
  - `go test ./...` for unit, fast integration, and small smoke tests.
  - `go test -tags=e2e ./...` for the full acceptance matrix.

## Progress Tracking
- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with `+` prefix.
- Document blockers with `BLOCKED:` prefix.
- Keep the plan in sync with actual work.

## What Goes Where
- Implementation Steps: tasks achievable inside this repository.
- Post-Completion: manual or external-system work, without checkboxes.

## Implementation Steps

### Task 1: Bootstrap Project Tooling
**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `.gitignore`
- Create: `.golangci.yml`
- Create: `Makefile`
- Create: `LICENSE`
- Create: `README.md`
- Create: `cmd/llmgate/main.go`
- Create: `internal/version/version.go`
- Create: `internal/version/version_test.go`

- [x] Ensure direct dependencies are declared intentionally in `go.mod`, including `charm.land/huh/v2`, `github.com/tailscale/hujson`, `golang.org/x/term`, and `golang.org/x/sys`.
- [x] Add MIT license and initial README with project purpose, rolling `main` warning, build/test commands, and install command placeholders.
- [x] Add `Makefile` targets: `build`, `test`, `test-e2e`, `lint`, `fmt`, `check`, `clean`, and `.tools/bin` installation for pinned `golangci-lint v2.12.2`.
- [x] Add `.golangci.yml` compatible with golangci-lint v2.
- [x] Add version package with ldflags-backed `version`, `commit`, `date`, plus Go runtime and target OS/arch in `--version`.
- [x] Add initial CLI entrypoint supporting `--help`, `--version`, no-arg dispatch placeholder, and non-zero error handling.
- [x] Add tests for version formatting and CLI argument behavior.
- [x] Run `make fmt`, `make test`, and `make lint` for this task.

### Task 2: Core Domain Model and Redaction
**Files:**
- Create: `internal/core/vars.go`
- Create: `internal/core/values.go`
- Create: `internal/core/diagnostic.go`
- Create: `internal/redact/redact.go`
- Create: `internal/redact/redact_test.go`
- Create: `internal/core/core_test.go`

- [x] Define managed variables, required values, behavior/privacy defaults, write target types, source labels, resolved config structures, and diagnostic status model.
- [x] Implement aggregate diagnostic severity ordering: `OK < SKIP < WARN < FAIL`.
- [x] Implement secret masking for exact known secrets, bearer headers, `x-litellm-api-key`, `sk-...`, and `ANTHROPIC_AUTH_TOKEN` assignment forms.
- [x] Implement home path shortening with platform-aware separators.
- [x] Add tests covering redaction hard requirements, short/long token display, path shortening, and diagnostic aggregation.
- [x] Run focused package tests before moving on.

### Task 3: System Abstractions and Platform Paths
**Files:**
- Create: `internal/system/system.go`
- Create: `internal/system/paths.go`
- Create: `internal/system/paths_test.go`
- Create: `internal/system/terminal.go`
- Create: `internal/system/command.go`
- Create: `internal/system/env_unix.go`
- Create: `internal/system/env_windows.go`
- Create: `internal/system/env_other.go`
- Create: `internal/system/fake_test.go`

- [x] Define injectable interfaces for filesystem operations, process environment, command execution, terminal interactivity, current OS, home directory, working directory, and Windows user environment access.
- [x] Implement production adapters for real filesystem, terminal detection, current env, and `claude --version`.
- [x] Implement platform path detection for Claude settings, shell profiles, VS Code, Cursor, project settings, and Windows user environment.
- [x] Implement Windows user-scoped environment adapter behind Windows build tags.
- [x] Add fake system implementation for unit and e2e tests.
- [x] Add tests for macOS, Linux, and Windows path detection, shell profile selection, unknown shell handling, and IDE target detection rules.
- [x] Run focused system tests and cross-compile Windows packages where possible.

### Task 4: JSONC Settings Parser and Writer
**Files:**
- Create: `internal/settings/jsonc.go`
- Create: `internal/settings/claude.go`
- Create: `internal/settings/ide.go`
- Create: `internal/settings/settings_test.go`
- Create: `internal/settings/testdata/`

- [x] Parse JSONC using `hujson`, requiring object roots for Claude and IDE settings.
- [x] Read only string values under Claude `env`.
- [x] Read IDE `claudeCode.environmentVariables` entries with string `name` and `value`, plus string `claudeCode.selectedModel`.
- [x] Upsert Claude managed values under `env`, preserving unrelated top-level keys and rejecting malformed settings.
- [x] Upsert IDE selected model and environment variable entries by name, preserving unrelated settings and entries.
- [x] Ensure trailing newline and reasonable JSONC/comment preservation.
- [x] Add tests for valid JSONC, comments, malformed files, non-object roots, unrelated preservation, idempotency, and token redaction in error paths.
- [x] Run focused settings tests.

### Task 5: Shell Profile Parser and Writer
**Files:**
- Create: `internal/shell/profile.go`
- Create: `internal/shell/parse_posix.go`
- Create: `internal/shell/parse_fish.go`
- Create: `internal/shell/write.go`
- Create: `internal/shell/profile_test.go`
- Create: `internal/shell/testdata/`

- [x] Implement strict line-based parsing for simple active managed assignments.
- [x] Ignore commented assignments for effective values.
- [x] Detect duplicate active simple values for the same managed variable.
- [x] Detect dynamic or complex assignments and mark them for manual review without modifying them.
- [x] Preserve unrelated content, existing comments, commented assignments, unrelated variables, and inline comments on simple assignments.
- [x] Write POSIX `export NAME='value'` and fish `set -x NAME 'value'` syntax with safe quoting.
- [x] Update simple active assignments in place, append missing values during setup, and avoid appending missing values during repair mode.
- [x] Add tests for zsh/bash/fish read/write, comments, inline comments, duplicates, dynamic assignments, idempotency, and no legacy managed block behavior.
- [x] Run focused shell tests.

### Task 6: Gateway Client and Model Recommendation
**Files:**
- Create: `internal/gateway/client.go`
- Create: `internal/gateway/cache.go`
- Create: `internal/gateway/models.go`
- Create: `internal/gateway/recommend.go`
- Create: `internal/gateway/client_test.go`
- Create: `internal/gateway/recommend_test.go`

- [x] Implement model list URL normalization, including `/v1/models`, base paths, `/v1` suffixes, query/hash removal, trailing slash handling, and `/models` fallback only on 404.
- [x] Implement model list requests with required headers and sorted unique model IDs.
- [x] Classify auth, HTTP, invalid JSON, empty models, network/timeout, and invalid URL failures.
- [x] Sanitize and truncate response details to 500 characters.
- [x] Implement chat completion model probe with `ping` and `max_tokens: 1`.
- [x] Implement success/failure cache keyed by normalized URL, token, and model, with bypass paths for retry/reselection.
- [x] Implement recommendation logic for primary, haiku, sonnet, and opus according to spec ordering.
- [x] Add httptest-based tests for all gateway acceptance scenarios and recommendation ordering.
- [x] Run focused gateway tests.

### Task 7: Config Collection and Resolution
**Files:**
- Create: `internal/config/read.go`
- Create: `internal/config/resolve.go`
- Create: `internal/config/conflicts.go`
- Create: `internal/config/sources.go`
- Create: `internal/config/config_test.go`

- [x] Implement post-approval read of Claude user settings, shell profile or Windows user env, current process environment, IDE settings, and project settings.
- [x] Ensure startup decline path performs no reads, existence checks, commands, HTTP calls, writes, or env changes.
- [x] Resolve persisted global sources with shell/Windows user env priority over Claude user settings.
- [x] Resolve current effective sources with current process env priority over Claude user settings.
- [x] Keep IDE and project settings as side contexts that do not overwrite global resolution.
- [x] Detect persisted conflicts, duplicate shell values, dynamic/complex shell assignments, current-only and persisted-only values.
- [x] Add tests for source precedence, shadowed values, current vs persisted differences, project override comparisons, IDE drift, and malformed source severity.
- [x] Run focused config tests.

### Task 8: Diagnostics Engine and Report Rendering
**Files:**
- Create: `internal/diagnose/engine.go`
- Create: `internal/diagnose/report.go`
- Create: `internal/diagnose/sections.go`
- Create: `internal/diagnose/diagnose_test.go`
- Create: `internal/diagnose/report_test.go`

- [x] Implement diagnostic sections for Claude Code CLI, Claude Code Config, Config Source Conflicts, Runtime Environment, Config Sources, Project Overrides, Gateway contexts, Models, Model Probes, IDE Config, Project Config Validation, IDE Config Validation, and Write Targets.
- [x] Validate current and persisted gateway contexts, downgrading failures to `WARN` when another context is usable.
- [x] Validate project and IDE gateway/model contexts separately when network checks are enabled.
- [x] Render stable copy-paste diagnostic reports with redacted secrets and shortened home paths.
- [x] Implement repairable stale shell model warning detection.
- [x] Add tests covering OK, SKIP, WARN, FAIL aggregation, multi-context gateway behavior, source issue severities, report format, and redaction.
- [x] Run focused diagnostics tests.

### Task 9: Apply Plan and Writers
**Files:**
- Create: `internal/apply/plan.go`
- Create: `internal/apply/diff.go`
- Create: `internal/apply/write.go`
- Create: `internal/apply/backup.go`
- Create: `internal/apply/windows.go`
- Create: `internal/apply/apply_test.go`

- [x] Build setup apply plans for Claude settings, shell profile, Windows user environment, VS Code, Cursor, and manual shell setup target.
- [x] Build repair apply plans that update stale simple active shell assignments only.
- [x] Display old/new changes with `<unset>`, `<empty>`, masked secrets, shortened home paths, target titles, operations, warnings, and sensitivity flags.
- [x] Implement file backups as `<file>.llmgate.bak` or timestamped fallback.
- [x] Implement atomic replacement using temp file in the same directory where possible.
- [x] Set user-only permissions for sensitive files and backups on a best-effort basis.
- [x] Skip unchanged targets without rewriting or backing up.
- [x] Implement Windows user environment writes with no backup claim and changed-variable reporting.
- [x] Add tests for fresh setup, updates, idempotency, backups, malformed rejection, manual targets, repair mode, Windows env apply, and apply failure behavior.
- [x] Run focused apply tests.

### Task 10: Wizard Flow with `huh`
**Files:**
- Create: `internal/wizard/wizard.go`
- Create: `internal/wizard/prompts.go`
- Create: `internal/wizard/actions.go`
- Create: `internal/wizard/io.go`
- Create: `internal/wizard/wizard_test.go`
- Modify: `cmd/llmgate/main.go`

- [x] Implement non-interactive no-arg failure with a clear message.
- [x] Implement startup disclosure approval with strict decline/cancel no-read behavior.
- [x] Run initial diagnostics after approval.
- [x] Implement action menu with `Setup`, conditional `Repair warnings`, `Review details`, and `Exit`, including default highlighted action rules.
- [x] Implement existing token reuse/replacement flow, base URL prompt, gateway validation recovery, model recommendation/manual selection, probe validation, target selection, apply plan confirmation, rejected-plan return to targets, write results, and final diagnostics.
- [x] Implement cancellation behavior for every prompt according to spec.
- [x] Ensure no token appears in terminal output, errors, reports, apply plan, or write results.
- [x] Add deterministic accessible-input tests for prompt branching and cancellation.
- [x] Run focused wizard tests.

### Task 11: Acceptance E2E Test Harness
**Files:**
- Create: `internal/e2e/harness_test.go`
- Create: `internal/e2e/fake_gateway_test.go`
- Create: `internal/e2e/wizard_accessible_test.go`
- Create: `internal/e2e/wizard_pty_test.go`
- Create: `internal/e2e/acceptance_test.go`
- Create: `internal/e2e/acceptance_helpers_test.go`

- [x] Build e2e harness with temp HOME/CWD, fake platform, fake command runner, fake Windows user env, fake gateway, and scripted wizard input.
- [x] Add accessible wizard e2e tests for all acceptance scenario groups in `docs/PROJECT_SPEC.md`.
- [x] Add real `xpty` smoke tests for no-arg interactive startup, password/token prompt, cancellation, and non-interactive failure.
- [x] Gate the full matrix behind `//go:build e2e` while keeping a small smoke subset in default tests.
- [x] Ensure tests assert no secret leakage and no forbidden reads/writes before startup approval.
- [x] Run `go test ./...` and `go test -tags=e2e ./...`.

### Task 12: CI Workflows
**Files:**
- Create: `.github/workflows/ci.yml`
- Create: `.github/dependabot.yml`
- Modify: `README.md`

- [ ] Add CI matrix for Linux, macOS, and Windows using `actions/checkout@v6` and `actions/setup-go@v6` with fixed Go `1.26.3`.
- [ ] Run `make fmt`, `make lint`, `make test`, and `make test-e2e` in CI.
- [ ] Use `golangci/golangci-lint-action@v9` with pinned golangci-lint `v2.12.2`.
- [ ] Add shellcheck for `scripts/install.sh` once that script exists.
- [ ] Add PowerShell smoke for `scripts/install.ps1` once that script exists.
- [ ] Add Dependabot configuration for GitHub Actions major-tag updates where useful, without changing the chosen major-tag style.
- [ ] Run local workflow-equivalent commands where possible.

### Task 13: Rolling `main` Release Workflow
**Files:**
- Create: `.github/workflows/release-main.yml`
- Create: `scripts/package.sh`
- Modify: `Makefile`
- Modify: `README.md`

- [ ] Add release workflow on push to `main`.
- [ ] Run `make check` before publishing release assets.
- [ ] Build six targets: `linux-amd64`, `linux-arm64`, `darwin-amd64`, `darwin-arm64`, `windows-amd64`, `windows-arm64`.
- [ ] Set ldflags for `version=main`, commit SHA, and build date.
- [ ] Package Unix targets as `.tar.gz` and Windows targets as `.zip`, including binary, `README.md`, and `LICENSE`.
- [ ] Generate `checksums.txt` with SHA-256 for all archives.
- [ ] Publish or update a rolling prerelease named/tagged `main` using `gh release` and `GITHUB_TOKEN`.
- [ ] Include commit SHA in artifact names or release notes.
- [ ] Add package script tests or dry-run checks where practical.

### Task 14: Install Scripts
**Files:**
- Create: `scripts/install.sh`
- Create: `scripts/install.ps1`
- Create: `scripts/install_test.go` or `internal/installtest/...`
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/release-main.yml`
- Modify: `README.md`

- [ ] Implement Unix install script that downloads rolling `main`, selects OS/arch, verifies `checksums.txt`, and installs to `/usr/local/bin` or `$HOME/.local/bin`.
- [ ] Support Unix env overrides: `LLMGATE_INSTALL_DIR`, `LLMGATE_ARCH`, and `LLMGATE_OS`; do not support SemVer version selection.
- [ ] Implement PowerShell install script that downloads rolling `main`, selects OS/arch, verifies `checksums.txt`, installs to `$env:LOCALAPPDATA\Programs\llmgate\bin`, and optionally adds User PATH via `LLMGATE_ADD_TO_PATH=1`.
- [ ] Add `-DryRun` or equivalent safe mode for CI smoke tests.
- [ ] Attach install scripts to the rolling `main` release.
- [ ] Add shellcheck and PowerShell smoke checks in CI.
- [ ] Document install commands and rolling release caveat in README.

### Task 15: Final Documentation and Acceptance Verification
**Files:**
- Modify: `README.md`
- Modify: `docs/PROJECT_SPEC.md` if implementation-driven clarifications are needed
- Modify: `docs/plans/20260512-llmgate-full-implementation.md`

- [ ] Update README with usage, privacy behavior, supported platforms, write targets, diagnostics, install scripts, build/test commands, and rolling release warning.
- [ ] Verify `docs/PROJECT_SPEC.md` matches implementation scope, especially legacy managed blocks being out of scope.
- [ ] Run full local verification: `make check`.
- [ ] Run explicit cross-platform compile checks for all release targets.
- [ ] Verify all acceptance scenario groups are represented by unit, integration, or e2e tests.
- [ ] Move this plan to `docs/plans/completed/` after implementation is complete and verified.

## Technical Details
- CLI flow:
  - `llmgate` with no args starts wizard only in an interactive terminal.
  - `llmgate --help` prints help and exits `0`.
  - `llmgate --version` prints version, commit/date when available, Go version, and OS/arch.
- Main package layout:
  - `internal/core`: product constants and shared domain models.
  - `internal/redact`: redaction and display-safe formatting.
  - `internal/system`: platform and OS adapters plus test fakes.
  - `internal/settings`: Claude and IDE JSONC reads/writes.
  - `internal/shell`: shell profile reads/writes.
  - `internal/gateway`: LiteLLM-compatible HTTP validation, probes, caching, and recommendations.
  - `internal/config`: source collection and resolution.
  - `internal/diagnose`: diagnostics and report rendering.
  - `internal/apply`: apply plan generation and writes.
  - `internal/wizard`: `huh` prompts and setup orchestration.
  - `internal/e2e`: acceptance harness and e2e tests.
- Exit code policy:
  - `Configured` and `Configured with warnings`: `0`.
  - User-selected `Exit` without writing: `0`.
  - Startup disclosure decline/cancel: non-zero, after `No files were read or changed.`
  - Non-interactive no-arg failure: non-zero.
  - Final `Setup incomplete` / aggregate `FAIL`: non-zero.
- Security/privacy:
  - No telemetry.
  - No file logs.
  - No secret should appear in stdout, stderr, diagnostics, apply plans, write results, errors, or tests.

## Post-Completion

**Manual verification**:
- Run `llmgate` in a real terminal on macOS with a fake local gateway and inspect the full setup flow.
- Run the Windows installer and wizard manually on a Windows machine or VM.
- Confirm rolling `main` release assets install correctly from GitHub after the first push to `main`.

**External system updates**:
- Enable GitHub Actions for `github.com/r13v/llmgate` if not already enabled.
- Ensure repository workflow permissions allow creating/updating releases with `GITHUB_TOKEN`.
