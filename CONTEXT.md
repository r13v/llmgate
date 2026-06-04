# llmgate

This context describes the public language for the `llmgate` CLI product and its setup/update workflow.

## Language

**llmgate**:
A public CLI product for configuring Claude Code to use a LiteLLM-compatible gateway.
_Avoid_: LiteLLM CLI, litellm

**LiteLLM-compatible gateway**:
An HTTP gateway that exposes OpenAI-compatible model listing and chat completion surfaces.
_Avoid_: LiteLLM CLI

**One-line run**:
A copy/paste shell or PowerShell invocation that obtains `llmgate` and starts it.
_Avoid_: installer, bootstrapper

**Installed command**:
The user-local `llmgate` executable made available after a one-line run.
_Avoid_: cached binary

**Canonical installed command**:
The single user-local `llmgate` executable owned by `llmgate` that one-line run and update command are allowed to refresh.
_Avoid_: current executable, arbitrary binary

**Canonical install path**:
The fixed filesystem location of the canonical installed command for the current OS user.
_Avoid_: cache path, PATH lookup result

**Install metadata**:
A user-local ownership record proving that the canonical installed command was written by `llmgate`.
_Avoid_: cache entry

**User-local install scope**:
An installation scope that makes `llmgate` available to the current OS user without administrator privileges.
_Avoid_: system-wide install

**Manual PATH setup**:
A user-performed command-lookup setup step prompted only when the installed `llmgate` executable is outside the terminal command path.
_Avoid_: automatic shell profile editing

**Update command**:
The `llmgate update` command that refreshes the installed command.
_Avoid_: litellm update

**Rolling main build**:
The latest prerelease build published from the `main` branch.
_Avoid_: stable release, versioned channel

## Relationships

- A **One-line run** obtains the **Installed command** and starts **llmgate**.
- A **One-line run** refreshes the **Installed command** before starting **llmgate** when a newer build is available.
- The **Canonical installed command** lives at the **Canonical install path**.
- **Install metadata** records ownership of the **Canonical installed command**.
- The **Update command** refreshes the **Canonical installed command** only.
- An **Installed command** belongs to a **User-local install scope**.
- **Manual PATH setup** may be needed before an **Installed command** can be invoked by name.
- The **Update command** refreshes the **Installed command** from the **Rolling main build** without starting the setup wizard.
- **llmgate** configures Claude Code to use a **LiteLLM-compatible gateway**.

## Example dialogue

> **Dev:** "Should users run `litellm update` to refresh the CLI?"
> **Domain expert:** "No, the product command is **llmgate**; the refresh command is **llmgate update**."

## Flagged ambiguities

- "`litellm update`" was used to mean **Update command**; resolved: the canonical command is `llmgate update`.
- "Available in the terminal" can imply automatic shell profile changes; resolved: **Installed command** means the user-local executable, not hidden command-lookup mutation.
