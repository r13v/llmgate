# llmgate: technology-neutral specification

## Document Purpose

This document describes the `llmgate` product contract without depending on any programming language, runtime, build system, UI library, or test framework.

A coding agent should be able to implement an equivalent project on any technology stack from this specification. Command names, file paths, environment variable names, HTTP endpoints, and external product names are part of the product behavior, not recommendations for implementation technology.

This specification intentionally covers only the no-argument interactive setup wizard. Other command-line modes, subcommands, flags, package metadata, release automation, and implementation-specific developer tooling are outside this document and are not acceptance requirements.

## Product Summary

`llmgate` is a public interactive CLI wizard for configuring Claude Code to work through a LiteLLM-compatible gateway.

The CLI must:

- safely explain which local sources will be read;
- inspect the current Claude Code configuration;
- discover existing gateway, token, and model values;
- validate the token through a LiteLLM-compatible gateway;
- fetch the list of available models;
- recommend or let the user choose the Claude Code model mapping;
- show a change plan before writing anything;
- write settings to selected targets;
- create backups when changing files;
- rerun diagnostics after writing;
- render diagnostic details for review without leaking secrets.

## Product Principles

- No local files, environment variables, or user settings may be read before explicit approval in the interactive wizard.
- No files or user environment variables may be changed before explicit apply plan approval.
- All secrets must be masked in messages, errors, reports, and transcript-like output.
- Public examples, documentation, and fixtures must use only generic placeholders, for example `https://your-litellm-gateway.example.com` and fake tokens.
- Behavior must be cross-platform: macOS, Linux, and Windows.
- Repeating setup with the same values must be idempotent: no unnecessary rewrites and no unnecessary backups.
- The user must always see the reason for warnings and failures, plus a safe next step.

## Terms

- Gateway: a LiteLLM-compatible HTTP gateway that accepts OpenAI-compatible model listing and chat completion requests.
- Gateway token: the gateway API key. It is stored as `ANTHROPIC_AUTH_TOKEN`.
- Base URL: the LiteLLM proxy root URL, for example `https://your-litellm-gateway.example.com`.
- Claude Code user settings: the user file at `~/.claude/settings.json`.
- Project settings: Claude settings in the current project: `./.claude/settings.local.json` and `./.claude/settings.json`.
- Persisted config: configuration that will affect new terminal or IDE sessions.
- Current environment: values available to the current CLI process and current Claude Code settings.
- IDE settings: VS Code or Cursor user settings, if their config directories already exist.
- Write target: a location where the CLI can apply settings.
- Apply plan: a previewed, user-confirmed write plan.

## Public CLI Interface

### No-argument invocation

Running `llmgate` with no arguments starts the interactive setup wizard.

Requirements:

- the command must work only in an interactive terminal;
- if the terminal is not interactive, the command must fail with a clear message;
- the wizard must not read configuration before startup disclosure approval;
- the wizard must not write configuration before apply plan approval.

### Argument-bearing invocations

No argument-bearing invocation is required by this specification.

Requirements:

- the no-argument setup wizard must be fully usable without relying on any other command;
- this document does not require or forbid extra subcommands or flags;
- any implementation-specific extra command that prints errors should still redact secrets.

## Startup disclosure

Before reading local configuration, the interactive wizard must show the user:

- that the CLI helps configure Claude Code for a LiteLLM-compatible gateway;
- which local sources will be read;
- which local commands will be run;
- which network checks may be performed;
- that changes will not be written without separate apply plan approval.

Minimum read sources:

- `~/.claude/settings.json`;
- shell profile for zsh, bash, or fish on macOS/Linux;
- Windows user environment variables on Windows;
- VS Code user settings path;
- Cursor user settings path;
- `./.claude/settings.local.json`;
- `./.claude/settings.json`;
- current process environment.

Minimum local commands:

- `claude --version`;
- on Windows, reading user environment variables through the OS mechanism is also allowed.

Minimum network disclosure:

- if existing gateway credentials are found, the CLI may list models and send a tiny `ping` model test request to the configured gateway using the gateway token.

If the user declines:

- do not read files;
- do not check file or directory existence;
- do not run local commands;
- do not perform HTTP requests;
- do not write files or environment variables;
- show `No files were read or changed.`

Prompt cancellation at startup is equivalent to declining startup access.

## Managed configuration values

### Required values

| Name | Meaning | Secret |
| --- | --- | --- |
| `ANTHROPIC_AUTH_TOKEN` | Gateway API token | yes |
| `ANTHROPIC_BASE_URL` | LiteLLM-compatible gateway base URL | no |
| `ANTHROPIC_MODEL` | Primary Claude Code model | no |
| `ANTHROPIC_DEFAULT_HAIKU_MODEL` | Haiku tier model | no |
| `ANTHROPIC_DEFAULT_SONNET_MODEL` | Sonnet tier model | no |
| `ANTHROPIC_DEFAULT_OPUS_MODEL` | Opus tier model | no |

### Behavior and privacy defaults

These values are written during setup unless already up to date:

| Name | Value | Meaning |
| --- | --- | --- |
| `CLAUDE_CODE_ENABLE_TELEMETRY` | `0` | disable Claude Code telemetry |
| `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` | `1` | reduce Claude Code optional network traffic |
| `OTEL_METRICS_EXPORTER` | `otlp` | keep telemetry exporter explicit |
| `ANTHROPIC_DISABLE_NONESSENTIAL_TRAFFIC` | `1` | reduce optional Anthropic traffic |
| `DISABLE_PROMPT_CACHING_HAIKU` | `1` | disable LiteLLM prompt caching for Haiku tier |
| `DISABLE_PROMPT_CACHING_SONNET` | `1` | disable LiteLLM prompt caching for Sonnet tier |
| `DISABLE_PROMPT_CACHING_OPUS` | `1` | disable LiteLLM prompt caching for Opus tier |
| `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS` | `1` | disable Claude Code experimental beta headers |

### Default base URL

Default base URL placeholder:

```text
https://your-litellm-gateway.example.com
```

Do not use real internal hosts in docs, code, fixtures, tests, examples, or default prompts.

## Configuration sources

### Claude Code user settings

Path:

```text
~/.claude/settings.json
```

Expected shape:

```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "fake-token",
    "ANTHROPIC_BASE_URL": "https://your-litellm-gateway.example.com"
  }
}
```

Requirements:

- parser must accept JSON with comments (JSONC);
- the root value must be an object;
- values must be read only from string entries under `env`;
- unrelated settings must be preserved during writes;
- malformed persisted user settings are a `FAIL` source issue.

### Terminal shell profile on macOS/Linux

Detected path:

- zsh: `~/.zshrc`;
- fish: `~/.config/fish/config.fish`;
- bash on macOS: `~/.bash_profile` if it exists, otherwise `~/.bashrc`;
- bash on Linux: `~/.bashrc`;
- unknown shell: no writable shell profile target; show manual shell setup target.

Requirements:

- parse simple active assignments for managed variables;
- ignore commented assignments for effective values;
- detect dynamic or complex assignments and mark them for manual review;
- preserve unrelated shell content;
- preserve existing comments and unrelated environment variables;
- use shell-appropriate quoting when writing values;
- do not create duplicate managed values when a simple active assignment can be updated;
- do not modify dynamic or complex assignments automatically.
- do not create, detect, rewrite, or treat legacy managed shell blocks as special;
  only active line-based managed assignments participate in shell profile behavior.

POSIX-style output format:

```sh
export NAME='value'
```

Fish output format:

```fish
set -x NAME 'value'
```

### Windows user environment

Requirements:

- Windows uses user-scoped environment variables instead of shell rc files;
- reading and writing must use the OS user environment mechanism;
- apply plan must show old and new values;
- no file backup exists for Windows user environment changes, so the apply plan must state this clearly.

### Current process environment

Requirements:

- read current process environment after startup access is approved;
- only managed variable names are relevant;
- current environment values participate in effective current config;
- current-only values are warnings because new sessions may not inherit them.

### IDE settings

Supported IDE settings:

- VS Code user settings;
- Cursor user settings.

Paths:

- macOS VS Code: `~/Library/Application Support/Code/User/settings.json`;
- macOS Cursor: `~/Library/Application Support/Cursor/User/settings.json`;
- Linux VS Code: `~/.config/Code/User/settings.json`;
- Linux Cursor: `~/.config/Cursor/User/settings.json`;
- Windows VS Code: `%APPDATA%\Code\User\settings.json`, falling back to `~/AppData/Roaming/Code/User/settings.json` when `%APPDATA%` is unavailable;
- Windows Cursor: `%APPDATA%\Cursor\User\settings.json`, falling back to `~/AppData/Roaming/Cursor/User/settings.json` when `%APPDATA%` is unavailable.

Write target detection:

- add VS Code target only if the VS Code user settings directory already exists;
- add Cursor target only if the Cursor user settings directory already exists;
- do not create IDE config directories just to add a target.

Expected settings keys:

```json
{
  "claudeCode.selectedModel": "model-id",
  "claudeCode.environmentVariables": [
    { "name": "ANTHROPIC_MODEL", "value": "model-id" }
  ]
}
```

Requirements:

- parser must accept JSON with comments (JSONC);
- the root value must be an object;
- read managed values from `claudeCode.environmentVariables` entries with string `name` and `value`;
- read selected model from `claudeCode.selectedModel` when it is a string;
- malformed IDE settings are `WARN`, not `FAIL`, if global setup is otherwise valid;
- IDE config drift from terminal config should be reported as `WARN`;
- writes must update entries by variable name and preserve unrelated entries.

### Project settings

Paths in current working directory:

```text
./.claude/settings.local.json
./.claude/settings.json
```

Requirements:

- read project settings after startup approval;
- project settings are not normal write targets in setup;
- project values can override global Claude Code config in the current repo;
- differences from global effective config are `WARN`;
- malformed project settings are `WARN`, not `FAIL`, if global setup is otherwise valid;
- project gateway/model overrides should be validated separately when network checks are enabled.

## Config resolution

The product must distinguish persisted config from current effective config.

Persisted global sources:

1. Claude Code user settings.
2. Terminal shell profile on macOS/Linux, or Windows user environment on Windows.

Current effective sources:

1. Claude Code user settings.
2. Current process environment.

Source priority rules:

- shell profile or Windows user environment has priority over Claude user settings for new sessions;
- current process environment has priority over Claude user settings for the current session;
- if current environment value equals an already resolved value, keep the more specific existing source label;
- IDE and project settings are side contexts and should not overwrite global persisted/current resolution;
- project settings should be compared against global current values first, then persisted values.

Conflict detection:

- warn when the same persisted value differs across persisted sources;
- show the effective value for new sessions;
- show shadowed values;
- explain why a source wins;
- mask token values in conflict details;
- warn when a shell file contains multiple active simple values for the same managed variable;
- warn when shell assignments are dynamic or complex.

### Gateway validation contexts

Diagnostics must validate the current effective gateway context and, when it differs, the persisted gateway context for new sessions.

Context selection:

- current context uses Claude Code user settings plus current process environment;
- persisted context uses Claude Code user settings plus terminal shell profile or Windows user environment;
- persisted context is included only when at least one gateway or model value differs from the current context;
- gateway context values are `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_MODEL`, `ANTHROPIC_DEFAULT_HAIKU_MODEL`, `ANTHROPIC_DEFAULT_SONNET_MODEL`, and `ANTHROPIC_DEFAULT_OPUS_MODEL`.

Display requirements:

- if only one context is validated, use normal `Gateway`, `Models`, and `Model Probes` sections;
- if multiple contexts are validated, suffix section titles with the context name, for example `Gateway (current environment)` and `Gateway (persisted config for new sessions)`;
- if one context is fully usable, failures in the other context become `WARN` and must explain that another configuration context is valid;
- if no context is usable, gateway, model, and probe failures remain `FAIL`.

## Diagnostics model

Each check has:

- stable `id`;
- human-readable title;
- status;
- summary;
- optional details.

Statuses:

- `OK`: check passed;
- `SKIP`: intentionally skipped or not applicable;
- `WARN`: configuration may work, but user action may be needed;
- `FAIL`: setup is incomplete or unusable.

Aggregate status severity:

```text
OK < SKIP < WARN < FAIL
```

Empty diagnostics aggregate to `OK`.

## Diagnostic sections

### Claude Code CLI

Run:

```text
claude --version
```

Requirements:

- `OK` if command succeeds;
- `WARN` if command fails;
- command failure must not stop the rest of diagnostics.

### Claude Code Config

Checks required values:

- `OK` if all required values are present in current and persisted config;
- `WARN` if values exist only in current environment;
- `WARN` if values exist only in persisted config for new sessions;
- `FAIL` if required values are missing from current config and persisted config.

### Config Source Conflicts

Present only when conflicts exist.

Must cover:

- persisted sources with differing values;
- duplicate active shell values;
- dynamic or complex shell assignments.

### Runtime Environment

Requirements:

- `OK` when current environment/settings match persisted config for managed values;
- `OK` when there are no conflicting current environment values;
- `WARN` when a value exists only in current environment/settings;
- `WARN` when a value exists only in persisted config;
- `WARN` when current and persisted values differ.

### Config Sources

Present only when source issues exist.

Requirements:

- malformed persisted user settings produce `FAIL`;
- malformed project or IDE settings produce `WARN`.

### Project Overrides

Requirements:

- `OK none detected` when no project override differs from global config;
- `WARN` for each managed project value that differs from global effective config;
- include project location, global effective value/source, reason, and manual fix guidance.

### Gateway

Requirements:

- `SKIP configure token and base URL first` when either token or base URL is missing;
- `OK` when model listing succeeds;
- `FAIL` when current context gateway validation fails and no other context is usable;
- `WARN` when a context fails but another configuration context is usable.

### Models

For each configured model variable:

- `OK` if value is present and exists in gateway model list;
- `FAIL` if missing in the active failing context;
- `FAIL` if unavailable and no other context is usable;
- `WARN` if unavailable in one context but another context is usable.

### Model Probes

For each unique selected model:

- send a tiny chat completion request with message content `ping` and max output of 1 token;
- `OK` if gateway accepts the request;
- `FAIL` if probe fails and no other context is usable;
- `WARN` if probe fails in one context but another context is usable.

### IDE Config

Present only if relevant IDE settings exist.

Requirements:

- `OK` when IDE Claude settings match terminal config;
- `WARN` when IDE environment variables differ from terminal/global config;
- `WARN` when `claudeCode.selectedModel` differs from `ANTHROPIC_MODEL`;
- `WARN` when `claudeCode.selectedModel` differs from global primary model.

### Project Config Validation

Present only when project settings contain gateway/model context and network checks are enabled.

Requirements:

- validate project gateway overrides separately;
- warn if project token/base URL are insufficient for validation;
- warn if project-selected models are unavailable.

### IDE Config Validation

Present only when IDE settings contain gateway/model context and network checks are enabled.

Requirements:

- validate IDE-specific model values against the appropriate gateway context;
- warn when selected model or env models are unavailable;
- do not warn about missing IDE gateway credentials when global/current gateway credentials can validate the IDE selected model.

### Write Targets

Always list detected write targets.

Requirements:

- Claude Code user settings target always exists as writable target;
- macOS/Linux include terminal shell profile target if shell is detected;
- macOS/Linux include manual shell exports target if shell profile cannot be detected;
- Windows include Windows user environment target;
- IDE targets appear only if their config directories already exist.

## Initial wizard actions

After startup approval and initial diagnostics, the wizard shows an action menu.

Actions:

- `Setup`: enter or reuse gateway credentials, choose models, select write targets, and apply settings;
- `Repair warnings`: available only when diagnostics contain a writable stale shell model warning;
- `Review details`: print the full diagnostic report and exit without writing;
- `Exit`: exit without writing.

Requirements:

- if diagnostics are already `OK`, the initial highlighted action should be `Exit`;
- otherwise, the initial highlighted action should be `Setup`;
- cancelling the action prompt exits without writing;
- `Repair warnings` must not be shown when no writable repair plan exists;
- `Review details` must redact secrets and shorten home paths in the printed report.

## Gateway behavior

### Model list validation

Input:

- base URL;
- token;
- HTTP client;
- optional cache;
- optional timeout, default 10 seconds.

URL normalization:

- if base URL root is `https://gateway.example.com`, primary models URL is `https://gateway.example.com/v1/models`;
- if base URL ends with `/v1`, primary models URL is `<base>/models`;
- if base URL contains a path prefix, preserve the path prefix before appending `/v1/models` or `/models`;
- remove query string and hash;
- remove trailing slashes before appending endpoint.

Fallback:

- if primary `/v1/models` returns HTTP 404, retry `<root>/models`;
- if fallback succeeds, summary must mention `/models fallback`;
- do not fallback for auth errors or non-404 HTTP errors.

Request headers:

```text
Accept: application/json
Authorization: Bearer <token>
x-litellm-api-key: <token>
```

Response handling:

- HTTP 401 or 403: auth failure with message telling user the gateway rejected the token;
- other non-2xx HTTP: HTTP failure with status;
- invalid JSON: invalid-json failure;
- JSON with no usable `data[].id`: empty-models failure;
- network error or timeout: network failure;
- invalid base URL: invalid-url failure;
- success returns sorted unique non-empty string model IDs.

Error details:

- if response body contains useful text or JSON fields such as `detail`, `message`, `error`, or `error_description`, include sanitized details;
- details must be truncated to a bounded length, no more than 500 characters;
- details must redact known token and common token patterns.

### Model probe

URL normalization:

- if base URL root is `https://gateway.example.com`, completions URL is `https://gateway.example.com/v1/chat/completions`;
- if base URL ends with `/v1`, completions URL is `<base>/chat/completions`;
- if base URL contains a path prefix, preserve the path prefix before appending the chat completions endpoint;
- remove query string and hash.

Request:

```json
{
  "model": "selected-model-id",
  "messages": [{ "role": "user", "content": "ping" }],
  "max_tokens": 1
}
```

Headers:

```text
Accept: application/json
Authorization: Bearer <token>
Content-Type: application/json
x-litellm-api-key: <token>
```

Failure handling:

- HTTP 401 or 403: auth failure during probe;
- other non-2xx HTTP: HTTP failure during probe;
- network error or timeout: network failure;
- invalid base URL: invalid-url failure;
- all failure messages must redact secrets.

### Caching

Requirements:

- cache model list validations by normalized model URLs plus token;
- cache model probes by completions URL plus token plus model;
- cached successful results may be reused;
- cached failed results may be reused unless user explicitly chooses retry/reselect path that bypasses failed cache;
- summaries must mark cached results with `(cached)`;
- cache internals must not leak secrets through normal display or serialized diagnostics.

## Model recommendation and selection

### Recommendation

From the gateway model list, derive optional recommendations:

- Haiku tier: best available model whose ID includes both `claude` and `haiku`, case-insensitive;
- Sonnet tier: best available model whose ID includes both `claude` and `sonnet`, case-insensitive;
- Opus tier: best available model whose ID includes both `claude` and `opus`, case-insensitive;
- primary model: Sonnet recommendation if available, otherwise Opus, otherwise Haiku.

Best model ordering:

1. prefer model IDs without `.` over IDs with `.`;
2. prefer higher numeric version components found in the ID;
3. prefer IDs that do not contain `preview`, `beta`, or `experimental`;
4. use deterministic lexical ordering as final tie breaker.

If primary model cannot be found, do not show recommendation.

If a tier is missing but primary exists, recommended tier value should fall back to primary.

### Selection flow

If recommendation exists:

- show recommended primary, haiku, sonnet, and opus;
- ask whether to use recommended Claude model mapping;
- if accepted, use it without manual model prompts;
- if rejected, continue to manual selection.

Manual selection:

- choose primary model from gateway model list;
- ask whether to set advanced tier overrides;
- if advanced overrides are declined, use primary model for all tiers;
- if accepted, ask separately for haiku, sonnet, and opus models from gateway model list;
- preselect previously configured values when they are still available.

Validation before writes:

- every selected model must exist in the gateway model list;
- every unique selected model must pass model probe;
- setup-time selected model probes must bypass cached failed probe results;
- if validation fails, no files or user environment variables may be changed.

Probe failure recovery:

- `Choose models`: return to model selection, preserving previous selection defaults where useful and bypassing failed probe cache;
- `Edit token/base URL`: return to gateway input;
- `Exit`: stop without writing.

Gateway validation failure recovery:

- `Edit token/base URL`: return to gateway input;
- `Retry`: repeat gateway validation and bypass failed gateway cache;
- `Exit`: stop without writing.

## Setup wizard scenarios

### General cancellation behavior

Unless a scenario below says otherwise:

- cancelling a prompt exits the wizard without writing;
- cancelling gateway input exits before target selection;
- cancelling model selection exits before target selection;
- cancelling target selection exits without writing;
- cancelling apply plan confirmation exits without writing;
- gateway/model validation recovery prompts treat cancellation as `Exit`.

### Fresh successful setup

Preconditions:

- no managed config exists;
- gateway token and base URL are entered by user;
- gateway returns at least one usable model;
- selected models pass probes.

Flow:

1. Show startup disclosure.
2. User approves read-only diagnostics.
3. Run initial diagnostics.
4. Show missing required values.
5. User chooses `Setup`.
6. Prompt for token and base URL.
7. Validate gateway model list.
8. Recommend or select model mapping.
9. Probe selected models.
10. Detect write targets.
11. User selects targets.
12. Show setup apply plan with masked token values.
13. User approves.
14. Write settings.
15. Show write results.
16. Run final diagnostics.
17. Show `Configured` or `Configured with warnings`.
18. Remind user to restart terminal and IDE.

Expected writes:

- Claude Code user settings receives all managed values under `env`;
- macOS/Linux shell profile receives shell assignments;
- Windows user environment receives user-scoped variables;
- IDE settings receive selected model and environment variable entries only if selected as targets.

### Existing token reuse

Preconditions:

- existing token is found in current or persisted config.

Flow:

- show where existing token was found;
- show base URL source if known;
- ask `Use existing gateway token?`;
- if yes, do not prompt for token;
- if the prompt is cancelled, exit without writing;
- still prompt for base URL with existing/default value;
- continue gateway validation and model selection.

### Existing token replacement

If user declines existing token:

- treat token as missing;
- show token guidance;
- prompt for new token;
- use entered token for validation and writes.

### Gateway failure before writes

If gateway validation fails:

- show sanitized failure summary;
- offer edit, retry, exit;
- no target selection or apply plan should be shown until validation succeeds.

Failure classes to support:

- invalid URL;
- auth rejection;
- network/timeout;
- HTTP error;
- invalid JSON;
- empty model list.

### Selected model unavailable

If selected model is not in gateway model list:

- show message listing unavailable selected models;
- offer choose models, edit token/base URL, or exit;
- do not write.

### Selected model probe failure

If selected model exists but probe fails:

- show sanitized probe failure message;
- offer choose models, edit token/base URL, or exit;
- do not write.

### Target selection cancelled

If target selection is cancelled:

- stop without writing.

If user selects zero targets:

- show `No write targets selected. Nothing was changed.`;
- stop without writing.

Target selection requirements:

- all writable targets are selected by default;
- manual targets are shown as manual setup messages and are not selectable;
- the target selection label must include the target title and path, or `user environment` for Windows user environment.

### Apply plan rejected

If user answers no at apply plan confirmation:

- return to target selection;
- do not write;
- allow user to select targets again and confirm a new plan.

If user cancels apply plan:

- exit without writing.

### Idempotent rerun

Preconditions:

- all selected targets already contain the same intended values.

Requirements:

- apply plan shows `no changes needed`;
- apply results report skipped/up to date;
- no files are rewritten;
- no backups are created;
- final diagnostics still run.

### Updating existing files

Preconditions:

- target files exist with some old managed values and unrelated content.

Requirements:

- apply plan shows old-to-new changes;
- token old/new values are masked;
- unrelated content is preserved;
- existing file is backed up before replacement;
- backup path is shown after writing;
- file is replaced atomically where possible.

### Repair warnings

Repair warnings is an action shown only when diagnostics include repairable stale shell model warnings.

Repairable warning:

- model variable is unavailable;
- source location is a shell rc path;
- current effective value for the same variable exists and differs from stale shell value.

Flow:

1. User chooses `Repair warnings`.
2. CLI builds repair apply plan.
3. Plan updates stale simple active shell assignments to effective current model values.
4. Plan does not add missing shell assignments.
5. Dynamic or complex assignments are skipped with warning.
6. User confirms plan.
7. CLI writes shell file with backup if changed.
8. CLI reruns diagnostics.
9. CLI shows final result.

If no writable repair plan exists:

- show `No repairable warnings found.`

### Review details

If user chooses `Review details`:

- print full diagnostic report;
- exit wizard without writing.

## Apply plan

Plan fields per target:

- target title;
- target path or `user environment`;
- operation;
- list of old/new changes;
- warnings;
- whether content is sensitive.

Operations:

- `create file`;
- `update file`;
- `set Windows user environment`;
- `no changes needed`;
- `manual setup required`.

Plan requirements:

- setup plan must state it writes gateway credentials, model mapping, and privacy/traffic defaults;
- repair plan must state it updates stale shell model assignments;
- update-file plan must say a `.llmgate.bak` backup path will be reported after writing;
- sensitive file plans must say files are written with user-only permissions when possible;
- Windows user environment plan must warn that no file backup is created and old/new values are shown instead;
- if there are no changes for a target, show `changes: none`;
- display values:
  - unset as `<unset>`;
  - empty strings as `<empty>`;
  - secret values masked;
  - home path shortened to `~`.

## Write behavior

### File backups

When replacing an existing file:

- first backup path: `<file>.llmgate.bak`;
- if that exists, timestamped backup path: `<file>.llmgate.YYYYMMDD-HHMMSS.bak`;
- backup contains original content exactly;
- backup path is reported after write.

### Atomic file replacement

When possible:

- write new content to a temporary file in the same directory;
- rename temporary file over final path;
- create parent directories for Claude user settings and shell profile targets;
- for sensitive files and backups, set user-only permissions best effort.

### Claude user settings write

Requirements:

- create file if missing;
- if file exists, preserve unrelated top-level keys;
- upsert all managed values under `env`;
- preserve comments when supported by settings parser/writer;
- ensure trailing newline;
- reject malformed settings instead of overwriting them.

### IDE settings write

Requirements:

- create settings file if directory exists and file is missing;
- upsert `claudeCode.selectedModel` to primary model;
- upsert `claudeCode.environmentVariables` entries by variable name for every managed value in the setup apply values, including gateway credentials, model mapping, and behavior/privacy defaults;
- preserve unrelated settings and unrelated environment variable entries;
- ensure trailing newline;
- reject malformed settings instead of overwriting them.

### Shell profile write

Requirements:

- create file if missing;
- write all selected managed values using shell-appropriate syntax;
- update simple active existing assignments in place;
- preserve inline comments on simple assignments;
- preserve commented assignments unchanged;
- preserve unrelated variables and unrelated content;
- leave dynamic or complex assignments unchanged and warn;
- append missing simple assignments at end for normal setup;
- do not append missing values during repair-warnings mode.

Examples of dynamic or complex assignments that must not be changed automatically:

- command substitutions;
- multi-word unquoted values;
- `declare` or `typeset` forms;
- export-only declarations;
- conditional fish assignments;
- shell expressions with multiple assignments on one line.

### Windows user environment write

Requirements:

- set each changed managed variable in the user environment scope;
- skip unchanged values;
- report each changed variable;
- do not claim a file backup exists;
- if write fails, report failure and stop.

## Final result

After successful apply, rerun diagnostics.

Final labels:

- aggregate `OK`: `Configured`;
- aggregate `WARN` or `SKIP`: `Configured with warnings`;
- aggregate `FAIL`: `Setup incomplete`.

For `Configured` and `Configured with warnings`, show:

```text
Restart your terminal and IDE for changes to take effect.
```

If final result is incomplete and details have not already been shown, print `WARN` and `FAIL` diagnostic details before the outro.

## Diagnostic report format

Report shape:

```text
llmgate diagnosis: <STATUS>

*<Section Title>*
- <STATUS>: <summary>
  - <detail>
```

Requirements:

- include every diagnostic section and check;
- redact secrets;
- shorten home paths to `~`;
- output must be stable enough for issue/support copy-paste.

## Secret and path redaction

Redact:

- exact known secrets;
- `Bearer <token>`;
- `x-litellm-api-key: <token>`;
- `sk-...` token-like values;
- `ANTHROPIC_AUTH_TOKEN=<value>` and `ANTHROPIC_AUTH_TOKEN: <value>`;
- token echoes in gateway error details.

Secret value display:

- empty secret: `<empty>`;
- short non-`sk-` secret: `***`;
- long non-`sk-` secret: `***` plus last four characters;
- short `sk-` secret: `sk-[redacted]`;
- long `sk-` secret: `sk-...` plus last four characters.

Home path display:

- replace the user's home path with `~`;
- support path separators on all target platforms.

Hard requirement:

- full token text must never appear in terminal output, diagnostic report, thrown error message, gateway failure details, apply plan, or write results.

## Public content rules

Do not include:

- internal company names;
- internal support channels;
- staff names;
- real internal URLs or hostnames;
- real tokens.

Use placeholders:

- `https://your-litellm-gateway.example.com`;
- `https://gateway.example.com`;
- `sk-test-token-1234567890`;
- `<token>`;
- `<gateway>`.

## Acceptance scenarios

An implementation is behaviorally equivalent when these scenarios pass on supported platforms.

### CLI command scenarios

- Running `llmgate` in an interactive terminal starts setup wizard.
- Running `llmgate` in a non-interactive terminal fails with a clear message.
- Argument-bearing invocations are outside this specification and have no acceptance requirement here.
- Error messages redact token-like text.

### Privacy scenarios

- Declining startup disclosure causes no reads, no existence checks, no commands, no network calls, and no writes.
- Token entered during setup does not appear in terminal output.
- Token returned by gateway error body is redacted.
- Home path is redacted to `~` in display output.

### Gateway scenarios

- Valid gateway root lists models through `/v1/models`.
- Base URL ending in `/v1` normalizes to `/v1/models`.
- HTTP 404 from `/v1/models` falls back to `/models`.
- HTTP 401/403 is classified as auth failure.
- HTTP 5xx is classified as HTTP failure and includes sanitized details.
- Network failure is classified as network failure.
- Invalid JSON is classified as invalid-json failure.
- Empty `data` model response is classified as empty-models failure.
- Model list response is deduplicated and sorted.
- Model probe sends a `ping` chat completion request.
- Cached model/probe successes are reused and labeled cached.
- User retry bypasses cached gateway failure.
- User model reselection bypasses cached probe failure.

### Model selection scenarios

- Recommended mapping is offered when Claude haiku/sonnet/opus tier models exist.
- Sonnet is preferred as primary when available.
- Manual primary selection works when recommendation is declined.
- Advanced tier overrides allow separate haiku, sonnet, and opus choices.
- Unavailable selected model blocks writes.
- Failed selected model probe blocks writes.

### macOS/Linux write scenarios

- Fresh setup creates Claude user settings and detected shell profile.
- zsh shell profile writes POSIX export assignments.
- bash shell profile uses `.bash_profile` on macOS when present, otherwise `.bashrc`.
- fish shell profile writes fish `set -x` assignments.
- Existing files are updated without losing unrelated content.
- Existing files get `.llmgate.bak` backups.
- Idempotent rerun does not rewrite files and does not create backups.
- Malformed Claude user settings blocks overwriting that file.
- Dynamic shell assignment is preserved and warned about.

### Windows write scenarios

- Fresh setup writes Claude user settings and Windows user environment variables.
- Windows apply plan shows old/new environment values.
- Windows apply plan warns that file backups do not apply to user environment updates.
- Unchanged Windows user environment values are skipped.

### IDE scenarios

- VS Code target appears only when its user settings directory exists.
- Cursor target appears only when its user settings directory exists.
- IDE writes update `claudeCode.selectedModel`.
- IDE writes upsert environment variable entries by name.
- IDE writes preserve unrelated settings and entries.
- IDE selected model drift is reported as warning.
- IDE unavailable selected model is reported as warning during validation.

### Project settings scenarios

- Project override differing from global config is reported as warning.
- Malformed project settings are warning when global setup is valid.
- Project gateway override with invalid token is validated separately and reported as warning.
- Project settings are not offered as normal write targets.

### Repair scenarios

- Repair warnings action appears for stale shell model warnings.
- Repair warnings action is hidden for non-repairable warnings.
- Repair warnings action is hidden for JSON settings model warnings.
- Repair updates simple stale shell model assignment to effective current value.
- Repair preserves commented stale assignment.
- Repair skips dynamic stale assignment and does not add missing variables.
- Cancelled repair plan writes nothing.

### Final diagnostics scenarios

- After writes, diagnostics are rerun.
- Fully valid setup ends with `Configured`.
- Setup with project/IDE warnings ends with `Configured with warnings`.
- Setup with remaining failures ends with `Setup incomplete`.
- Successful or warning final result reminds user to restart terminal and IDE.

## Implementation freedom

Any implementation technology is acceptable if the observable behavior above is preserved:

- any programming language;
- any build system;
- any interactive prompt library or custom terminal UI;
- any JSON-with-comments parser/writer;
- any HTTP client;
- any test framework.

The implementation must still preserve the no-argument public CLI contract, configuration contracts, redaction behavior, cross-platform paths, gateway HTTP behavior, setup scenarios, and acceptance outcomes.
