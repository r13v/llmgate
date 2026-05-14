# Final Diagnostics New Session Mode

## Overview
- Change setup final diagnostics so they validate the configuration that will be active in a new terminal or IDE session after restart, instead of reusing the already-running `llmgate` process environment.
- Solve the confusing post-setup warning where `.zshrc` is updated successfully, but final diagnostics still validate the stale `ANTHROPIC_AUTH_TOKEN` inherited by the current process.
- Keep real future-context warnings visible: IDE overrides, project overrides, malformed sources, invalid tokens, unavailable models, failed probes, and unreadable files must still affect the final `OK/WARN/FAIL`.
- Show differences in the already-running shell as an informational note only, not as an aggregated diagnostic warning or failure.

## Context
- Files/components involved:
  - `internal/wizard/actions.go`: applies plans, reruns final diagnostics, prints setup outcome.
  - `internal/config/read.go`: reads persisted sources and current process environment.
  - `internal/config/resolve.go`: resolves persisted/current contexts and computes runtime, IDE, and project differences.
  - `internal/diagnose/engine.go`: runs diagnostics, evaluates gateway/model/probe contexts, builds findings.
  - `internal/diagnose/sections.go`: renders mode-sensitive runtime and context wording.
  - `internal/gateway/client.go` and `internal/gateway/cache.go`: already support bypassing failed cache entries through request options.
  - `internal/wizard/wizard_test.go`, `internal/config/config_test.go`, `internal/diagnose/diagnose_test.go`, `internal/e2e/acceptance_test.go`: expected primary test coverage.
  - `README.md` and `docs/PROJECT_SPEC.md`: user-facing semantics for final diagnostics.
- Related patterns found:
  - `config.Read(..., approved)` remains the privacy/read boundary.
  - `diagnose.Run` is the aggregation boundary for both initial and final diagnostics.
  - Diagnostics currently resolve `current environment` from Claude user settings plus `SourceCurrentEnv`.
  - Persisted config currently resolves from Claude user settings plus shell profile or Windows user environment.
  - Gateway retry paths already bypass cached failures with `gateway.RequestOptions{BypassFailedCache: true}`.
- Dependencies identified:
  - No new third-party dependency is needed.
  - Existing fake filesystem/platform/gateway harnesses can cover the behavior without touching local state or external network.

## Review Handoff
- Original request: after setup writes new values to `.zshrc`, final diagnostics should not warn based on stale env vars inherited by the already-running shell.
- Key decisions made during planning:
  - Final diagnostics after setup validate `new terminal session`, not `current environment`.
  - Final diagnostics use actually reread persisted sources after writes, not the apply plan or prompt values as truth.
  - The already-running process environment is excluded from the final aggregated result.
  - The old process environment may be compared separately for a `Current terminal note`.
  - IDE and project drift compare against `new terminal session` in final mode.
  - Runtime text is mode-sensitive and must not claim `current environment` when validating a new session.
  - Final gateway/model/probe checks bypass cached failures.
  - If IDE overrides remain stale because the user did not select IDE targets, final result stays `WARN`.
- Explicit non-goals:
  - Do not execute or `source` shell startup files to mutate the current process or simulate a shell session.
  - Do not suppress real future-session warnings.
  - Do not change the startup privacy approval boundary.
  - Do not change the initial diagnostics semantics.
  - Do not change the repair action's final diagnostics semantics in this pass; repair should keep the existing current-process mode unless a separate repair-specific change is requested.
- Open questions or assumptions:
  - Testing approach is regular code-first with tests included in each task.
  - The final note should be shown only when the already-running process environment differs from the new-session resolved config for managed values.

## Development Approach
- Testing approach: Regular code-first with tests in each task.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- Every code-change task must include new or updated tests.
- All tests for a task must pass before starting the next task.
- Update this plan when scope changes during implementation.

## Testing Strategy
- Unit tests required for every code-change task.
- E2E acceptance tests required for the interactive setup final diagnostics behavior.
- Cover success, warning, and cache-edge cases.
- Prefer existing fake filesystem, fake process environment, and fake gateway harnesses over real local state.

## Progress Tracking
- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with `+` prefix.
- Document blockers with `BLOCKED:` prefix.
- Keep the plan in sync with actual work.

## What Goes Where
- Implementation Steps: tasks achievable within this codebase.
- Post-Completion: manual or external-system work, without checkboxes.

## Implementation Steps

### Task 1: Add Current Context Resolution Modes
**Files:**
- Modify: `internal/config/resolve.go`
- Modify: `internal/config/config_test.go`

- [x] Add `CurrentMode` and `ResolveOptions`, with default `CurrentModeProcessEnvironment`.
- [x] Keep `Resolve(read)` as the default wrapper so existing callers keep initial diagnostics behavior.
- [x] Add `ResolveWithOptions(read, opts)` for mode-aware resolution.
- [x] In `CurrentModeProcessEnvironment`, preserve current behavior: current context is Claude user settings plus current process environment, named `current environment`.
- [x] In `CurrentModeNewSession`, resolve current context from persisted global sources, named `new terminal session`.
- [x] Ensure runtime, IDE drift, and project override comparisons use the selected current context name.
- [x] Add config tests covering default behavior and new-session behavior when current process env is stale but `.zshrc` has the new value.
- [x] Run relevant tests and confirm they pass before next task: `go test ./internal/config`.

### Task 2: Make Diagnostics Mode-Aware and Bypass Final Failed Cache
**Files:**
- Modify: `internal/diagnose/engine.go`
- Modify: `internal/diagnose/sections.go`
- Modify: `internal/diagnose/diagnose_test.go`
- Modify: `internal/diagnose/findings.go`
- Modify: `internal/diagnose/findings_test.go`

- [ ] Add diagnostic options for current context mode and gateway failed-cache bypass.
- [ ] Have `diagnose.Run` call mode-aware config resolution.
- [ ] Pass `BypassFailedCache` through `ListModels` and `ProbeModel` evaluations when requested.
- [ ] Pass `BypassFailedCache` through side-context `ListModels` calls in project and IDE validation when requested.
- [ ] Replace hard-coded `current environment` assumptions with the selected current context name across diagnostic sections, checks, details, and findings.
- [ ] Audit `buildClaudeConfigSection`, `buildRuntimeSection`, `runtimeSummary`, `runtimeDetails`, `repairableStaleShellModel`, context constants, and finding evidence/title text for mode-sensitive wording.
- [ ] Verify gateway/model/probe section titles and evidence say `new terminal session` in final mode.
- [ ] Add a rendered-output test proving final diagnostics do not say `current environment` in new-session mode except in the separate current-terminal note owned by wizard output.
- [ ] Add unit tests proving new-session diagnostics do not evaluate stale process env as the gateway context.
- [ ] Add unit tests proving final diagnostics bypass cached gateway failures.
- [ ] Add unit tests proving side-context project/IDE validation bypasses cached gateway failures in final mode.
- [ ] Add unit tests proving IDE drift compares against `new terminal session` in final mode.
- [ ] Run relevant tests and confirm they pass before next task: `go test ./internal/diagnose`.

### Task 3: Use New-Session Diagnostics After Apply and Print Process Env Note
**Files:**
- Modify: `internal/wizard/actions.go`
- Modify: `internal/wizard/wizard_test.go`

- [ ] Add a mode/options parameter to `applyPlanAndFinalize` or split setup/repair finalization so setup can use new-session diagnostics without changing repair semantics.
- [ ] Change the setup path to rerun diagnostics in new-session mode with failed gateway cache bypass.
- [ ] Keep the repair path on the existing current-process final diagnostics mode unless this plan is explicitly expanded.
- [ ] Ensure final `OK/WARN/FAIL`, `Configured`, `Configured with warnings`, and `Setup incomplete` are based only on the new-session diagnostic result.
- [ ] Add a helper that compares the already-running process environment with the final new-session resolved config for managed values.
- [ ] Print a `Current terminal note` only when process env managed values differ from the final new-session values.
- [ ] Keep the restart line after successful or warning final results.
- [ ] Add wizard tests covering note visibility and absence when env already matches.
- [ ] Add a wizard test asserting the current-terminal note does not print raw old or new token values.
- [ ] Add or update a wizard test proving repair final diagnostics still use the existing current-process mode.
- [ ] Run relevant tests and confirm they pass before next task: `go test ./internal/wizard`.

### Task 4: Cover End-To-End Final Diagnostics Scenarios
**Files:**
- Modify: `internal/e2e/acceptance_test.go`
- Modify: `internal/e2e/wizard_accessible_test.go` if accessible output expectations change.

- [ ] Add or update acceptance coverage where setup updates `.zshrc`, current process env remains stale, and final diagnostics are `OK`.
- [ ] Assert the old cached gateway failure does not appear in final diagnostics.
- [ ] Assert final output uses `new terminal session`, not `current environment`, for the final gateway context.
- [ ] Assert `Current terminal note` is present for stale already-running process env.
- [ ] Assert the `Current terminal note` redacts secret values.
- [ ] Assert IDE drift warning disappears when IDE targets are updated to match the new session config.
- [ ] Assert IDE drift warning remains and final result is `WARN` when IDE overrides are left stale.
- [ ] Run relevant tests and confirm they pass before next task: `go test -tags=e2e ./internal/e2e`.

### Task 5: Verify Acceptance Criteria
**Files:**
- Modify: `docs/plans/20260514-final-diagnostics-new-session.md`

- [ ] Verify final diagnostics after setup validate reread persisted sources, not prompt/apply-plan values.
- [ ] Verify initial diagnostics still use current process environment.
- [ ] Verify old process env drift never changes final `OK/WARN/FAIL`.
- [ ] Verify real future-session warnings still affect final status.
- [ ] Verify repair final diagnostics retain their existing current-process behavior.
- [ ] Verify no secrets leak in the new note, diagnostics, tests, or failures.
- [ ] Run full default suite: `make test`.
- [ ] Run e2e suite: `make test-e2e`.

### Task 6: Final Documentation
**Files:**
- Modify: `README.md`
- Modify: `docs/PROJECT_SPEC.md`
- Modify: `docs/plans/20260514-final-diagnostics-new-session.md`

- [ ] Update docs to state final diagnostics after setup validate `new terminal session` config.
- [ ] Document that already-running shell differences are shown as a note only.
- [ ] Confirm docs still state that users should restart terminal and IDE for changes to take effect.
- [ ] Move this plan to `docs/plans/completed/` after implementation and verification.

## Technical Details
- Add a config-level mode instead of a wizard-only renderer workaround:

```go
type CurrentMode string

const (
	CurrentModeProcessEnvironment CurrentMode = "process-environment"
	CurrentModeNewSession         CurrentMode = "new-session"
)

type ResolveOptions struct {
	CurrentMode CurrentMode
}
```

- Preserve default compatibility:

```go
func Resolve(read ReadResult) Resolution {
	return ResolveWithOptions(read, ResolveOptions{})
}
```

- Expected resolution behavior:
  - `CurrentModeProcessEnvironment`: `Current` uses Claude user settings plus current process environment and name `current environment`.
  - `CurrentModeNewSession`: `Current` uses the same persisted global sources that determine new terminal behavior and name `new terminal session`.
- Add diagnostic options:

```go
type Options struct {
	NetworkChecks             bool
	Gateway                   gateway.Client
	CommandTimeout            time.Duration
	CurrentMode               config.CurrentMode
	BypassFailedGatewayCache  bool
}
```

- In final setup only, call diagnostics with:

```go
diagnose.Options{
	NetworkChecks:            r.network,
	Gateway:                  r.gateway,
	CommandTimeout:           r.command,
	CurrentMode:              config.CurrentModeNewSession,
	BypassFailedGatewayCache: true,
}
```

- Do not run `source ~/.zshrc`. Shell startup files may have side effects, dynamic commands, prompts, or long-running work, and a child shell cannot update the already-running parent process environment.
- Process env note should redact secrets and shorten paths using existing display/redaction helpers.

## Post-Completion
Items requiring manual intervention or external systems.

**Manual verification**:
- Run setup in a shell where `ANTHROPIC_AUTH_TOKEN` is stale, write a new `.zshrc` token, and confirm final diagnostics validate `new terminal session` and show only an informational current-terminal note.
- Run setup with stale Cursor/VS Code overrides intentionally left unselected and confirm final result is `WARN`.

**External system updates**:
- None expected.
