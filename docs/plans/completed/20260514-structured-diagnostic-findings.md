# Structured Diagnostic Findings

## Overview
- Add a structured diagnostic finding layer so `llmgate` can explain what is broken, why it matters, and what the user should do next without relying on raw check details.
- Solve the current first-screen diagnostic problem where the wizard prints every WARN/FAIL check verbatim, including repeated token mismatch warnings and long gateway error bodies.
- Keep full diagnostic depth available in `Review details`, while making the initial wizard summary actionable:
  - group repeated source/IDE token mismatch warnings;
  - promote gateway authentication failures into a clear primary finding;
  - connect gateway auth failures with token conflicts when both are present;
  - keep request URLs, failure kinds, HTTP status, and raw details as evidence, not as the headline.
- Ensure the first-screen summary never hides existing WARN/FAIL checks that are not yet represented by structured findings.
- Preserve existing redaction guarantees and terminal-only color/progress behavior.

## Context
- Files/components involved:
  - Existing diagnostics model: `internal/core/diagnostic.go`
  - Diagnostic construction: `internal/diagnose/engine.go`, `internal/diagnose/sections.go`
  - Full report rendering: `internal/diagnose/report.go`
  - Wizard first-screen summary: `internal/wizard/actions.go`
  - Gateway recovery prompt: `internal/wizard/prompts.go`
  - Terminal progress/color helper from current WIP: `internal/wizard/progress.go`
  - Tests: `internal/diagnose/diagnose_test.go`, `internal/diagnose/report_test.go`, `internal/wizard/wizard_test.go`, `internal/e2e/...`
- Related patterns found:
  - `diagnose.Result` currently aggregates sections/checks and has no explicit finding/cause/remediation model.
  - Gateway client errors already expose useful structure through `gateway.Error`: `Kind`, `Operation`, `StatusCode`, `URL`, `Detail`, `Cached`.
  - Config conflict and drift data is already structured before rendering: `config.ConflictIssue`, `config.RuntimeDifference`, `config.SideContextDifference`, and `config.SourceIssue`.
  - The wizard summary currently iterates WARN/FAIL checks directly, so duplicate source conflicts and IDE drift render as separate lines.
  - Full `diagnose.Render` is the right place to keep raw details; the wizard summary needs a separate actionable output shape.
  - Current uncommitted WIP already adds colorized statuses, network progress, and expanded gateway details. This plan treats that WIP as input that may be refactored into the structured finding layer.
- Dependencies identified:
  - No new third-party dependencies are expected.
  - Existing redaction package must remain the only path for user-facing secret masking.
  - Existing test command: `go test ./...`

## Scope Decisions
- Use Option B: introduce structured diagnostic findings instead of only renderer-level cleanup.
- Keep `core.DiagnosticSection` and `core.DiagnosticCheck` for full technical reports and existing tests.
- Add findings as a higher-level diagnostic product, not a replacement for checks in this change.
- Initial implementation targets the interactive wizard summary and gateway recovery prompt only; no JSON/API output is required.
- `diagnose.Render` / `Review details` will continue to render sections/checks in this first pass. Do not add a findings section to the full report unless the scope is explicitly updated and tested.
- Raw gateway response details should remain available in `Review details`, but the first wizard summary should show short cause/evidence/remediation text.
- The wizard summary must render findings first, then append any uncovered WARN/FAIL checks using `RelatedChecks` coverage so no diagnostic signal disappears.
- Do not weaken redaction. Every rendered finding must pass through the same known-secret and home-path sanitization path.

## Development Approach
- Testing approach: regular code-first with tests in each task.
- Complete each task fully before moving to the next.
- Make small, focused changes.
- Every code-change task must include new or updated tests.
- All tests for a task must pass before starting the next task.
- Update this plan when scope changes during implementation.

## Testing Strategy
- Unit tests required for every code-change task.
- Diagnostic tests should cover finding construction from structured gateway errors, token conflicts, IDE drift, and combinations of these signals.
- Wizard tests should cover first-screen summary formatting, grouping, color-disabled output, and redaction.
- E2E tests should cover a realistic token-conflict plus gateway-401 scenario similar to the observed screenshot.
- Cover success, error, edge, and privacy cases:
  - no findings when diagnostics are OK;
  - gateway auth failure without conflicts;
  - conflicts without gateway failure;
  - gateway auth failure with token conflicts producing a connected primary finding;
  - findings plus uncovered WARN/FAIL checks, proving uncovered checks remain visible;
  - side-context gateway validation failures for project, Cursor, and VS Code settings;
  - source issues, project overrides, unavailable models, and probe failures remaining visible even before they receive specialized findings;
  - long gateway detail truncation or relegation to evidence/details;
  - no raw token leakage.

## Progress Tracking
- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with `+` prefix.
- Document blockers with `BLOCKED:` prefix.
- Keep the plan in sync with actual work.

## What Goes Where
- Implementation Steps: tasks achievable within this codebase.
- Post-Completion: manual or external-system work, without checkboxes.

## Implementation Steps

### Task 1: Reconcile Current Diagnostic WIP
**Files:**
- Modify: `internal/diagnose/report.go`
- Modify: `internal/diagnose/sections.go`
- Modify: `internal/diagnose/engine.go`
- Modify: `internal/wizard/actions.go`
- Modify: `internal/wizard/prompts.go`
- Modify: `internal/wizard/progress.go`
- Modify: related tests touched in current WIP

Decision note: the working tree had no uncommitted WIP to reconcile. Keep the existing terminal-only progress/color helpers and full gateway evidence details in diagnostic checks. Defer structured first-screen rendering to the findings tasks instead of introducing a partial finding model here.

- [x] Review the current uncommitted color/progress/gateway-detail changes and decide which pieces stay as-is, move into findings, or get simplified.
- [x] Keep terminal progress/spinner behavior if tests confirm it does not pollute scripted or non-TTY output.
- [x] Keep full gateway evidence generation, but prepare to render it through findings rather than directly in the first wizard summary.
- [x] Run focused tests for `internal/diagnose` and `internal/wizard`.

### Task 2: Add Lean Finding Domain Model
**Files:**
- Modify: `internal/core/diagnostic.go`
- Modify: `internal/core/core_test.go`
- Create: `internal/diagnose/findings.go`
- Create: `internal/diagnose/findings_test.go`

- [x] Add a lean `core.DiagnosticFinding` model with fields for stable ID, status, title, summary, evidence lines, remediation, and related check IDs.
- [x] Avoid first-pass fields for tags, source lists, and separate cause text unless implementation proves they are needed for the current renderer.
- [x] Keep `internal/core` generic: no product-priority sorting, gateway-specific kinds, or UI ordering rules in this package.
- [x] Add finding ordering and coverage helpers in `internal/diagnose`, including a helper that derives covered check IDs from `RelatedChecks`.
- [x] Add unit tests for severity ordering, stable sorting, and empty/default behavior.
- [x] Run focused core tests before next task.

### Task 3: Build Findings And Preserve Side-Context Structure
**Files:**
- Modify: `internal/diagnose/engine.go`
- Modify: `internal/diagnose/sections.go`
- Modify: `internal/diagnose/diagnose_test.go`
- Modify: `internal/diagnose/report_test.go`

- [x] Extend `diagnose.Result` to include `Findings []core.DiagnosticFinding`.
- [x] Build gateway findings from `contextEvaluation.gatewayErr` and probe errors using `gateway.Error` structure.
- [x] Refactor project/IDE side-context validation so structured gateway failures are retained as side-validation results or converted into findings before only check-shaped data remains.
- [x] Build side-context gateway findings for project settings, Cursor settings, and VS Code settings when their gateway validation fails.
- [x] Build token conflict findings from `config.ConflictIssue`, grouping by managed variable and summarizing number of distinct values and involved sources.
- [x] Build IDE drift findings from `config.SideContextDifference`, grouping repeated IDE token differences across Cursor and VS Code.
- [x] Build connected findings when gateway auth fails and token conflicts or IDE drift involve `ANTHROPIC_AUTH_TOKEN`.
- [x] Keep existing sections/checks intact for full report rendering and backward-compatible tests.
- [x] Add tests for gateway-only, side-context-gateway-only, conflict-only, IDE-only, and combined gateway-auth-plus-token-conflict cases.
- [x] Run focused diagnose tests before next task.

### Task 4: Render Actionable Wizard Summary With Uncovered Check Fallback
**Files:**
- Modify: `internal/wizard/actions.go`
- Modify: `internal/wizard/wizard_test.go`
- Modify: `internal/e2e/wizard_accessible_test.go`
- Modify: `internal/e2e/acceptance_test.go`

- [x] Render structured findings first when findings are present.
- [x] Use `RelatedChecks` coverage to append uncovered WARN/FAIL checks after findings so legacy or not-yet-specialized diagnostics remain visible.
- [x] Use concise first lines such as `FAIL Gateway: token rejected` and `WARN Config: ANTHROPIC_AUTH_TOKEN differs across sources`.
- [x] Render evidence under each finding with short lines only: effective source, differing sources, request URL, HTTP status, and sanitized gateway message.
- [x] Render remediation lines separately from evidence, for example `fix: update the active token in ~/.zshrc or choose one source of truth`.
- [x] Fall back to the existing check-based summary when findings are unavailable.
- [x] Ensure color is applied only to status/message types on real TTY output and never in accessible scripted output.
- [x] Add tests matching the screenshot scenario and asserting grouped output rather than repeated raw checks.
- [x] Add tests proving an uncovered CLI warning, source issue, model warning, or probe failure still appears when at least one finding exists.
- [x] Run focused wizard and e2e tests before next task.

### Task 5: Preserve Full Details And Recovery Prompt Quality
**Files:**
- Modify: `internal/diagnose/report.go`
- Modify: `internal/wizard/prompts.go`
- Modify: `internal/diagnose/report_test.go`
- Modify: `internal/wizard/wizard_test.go`

- [x] Keep `Review details` rendering sections/checks with full evidence, including raw gateway detail where useful and redacted.
- [x] Add a shared gateway-error explanation helper that accepts an error or `gateway.Error` and returns concise cause/evidence/remediation text.
- [x] Use the shared helper from both finding construction and the gateway recovery prompt; do not pass `diagnose.Result` or findings into prompt code.
- [x] Add explicit tests that first-screen summary omits long raw gateway text while `Review details` still contains sanitized detail.
- [x] Add redaction tests for finding summary, cause, remediation, and evidence.
- [x] Run focused report and wizard tests before next task.

### Task 6: Verify Acceptance Criteria
**Files:**
- Modify: tests only as needed

- [x] Verify the screenshot-style scenario renders one primary gateway finding, one grouped config token finding, and one grouped IDE token finding.
- [x] Verify no raw token, bearer token, API key, or full home path leaks in first-screen summary or full details.
- [x] Verify OK diagnostics do not show noisy finding sections.
- [x] Verify network spinner/status output still appears only on interactive TTY.
- [x] Run final repository verification: `make test` and `make test-e2e`, or `make check` when lint/tooling is available.

### Task 7: Final Documentation
**Files:**
- Modify: `README.md` if user-facing diagnostic behavior needs documentation
- Modify: `docs/plans/20260514-structured-diagnostic-findings.md`

- [x] Update README diagnostics text only if the final output semantics changed enough to document.
- [x] Update this plan with any scope changes discovered during implementation.

Completion note: README diagnostics now documents the grouped first-screen finding behavior, uncovered check fallback, and `Review details` full-evidence path. No additional scope changes were discovered during final documentation.

## Technical Details
- Proposed `DiagnosticFinding` shape:
  - `ID string`
  - `Status core.DiagnosticStatus`
  - `Title string`
  - `Summary string`
  - `Evidence []string`
  - `Remediation string`
  - `RelatedChecks []string`
- Do not add first-pass tags/source fields unless a concrete renderer or grouping rule needs them.
- Finding priority ordering belongs in `internal/diagnose`, not `internal/core`.
- `RelatedChecks` is required for first-screen coverage: the wizard renderer must append uncovered WARN/FAIL checks after findings.
- Finding construction should happen near diagnostic construction, where structured inputs are still available:
  - gateway findings from `contextEvaluation`;
  - side-context gateway findings from project/IDE validation before `gateway.Error` is reduced to string details;
  - conflict findings from `resolution.Conflicts`;
  - IDE/project drift findings from `resolution.IDEDrift` and project override data.
- Wizard rendering should consume findings rather than reconstructing meaning from check summaries.
- Full detail rendering should keep using sections/checks in this first pass; adding a "Findings" section requires an explicit plan update and tests.
- Redaction and home path shortening must be applied at final render boundaries, not only when findings are built.
- Long details policy:
  - first-screen summary uses short gateway message/evidence;
  - full report may show sanitized and truncated gateway detail;
  - tests should assert the raw screenshot-style `Key Hash ... LiteLLM_VerificationTokenTable` noise does not become a first-screen headline.

## Post-Completion
Items requiring manual intervention or external systems.

**Manual verification**:
- Run the wizard in a real terminal with a deliberately invalid gateway token and conflicting Claude/Cursor/VS Code token values.
- Confirm the first screen reads as diagnosis and next action, not a raw trace.
- Open `Review details` and confirm the technical evidence is still available.

**External system updates**:
- None expected.

**Plan housekeeping**:
- Move this plan to `docs/plans/completed/` after implementation is complete.
