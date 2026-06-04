# User-local llmgate install

`llmgate` one-line run and `llmgate update` install to a fixed user-local command path: `$HOME/.local/bin/llmgate` on Unix and `%LOCALAPPDATA%\Programs\llmgate\llmgate.exe` on Windows. The install flow does not use administrator privileges, does not automatically edit shell profiles or User `PATH`, and only replaces a canonical command path that is empty or already owned by `llmgate` through install metadata written by the new installer; when the path is not command-invokable, it prints a manual PATH setup hint. This trades a little first-run friction and occasional manual conflict resolution for a simpler, less fragile install contract that matches the product's cautious approach to modifying user configuration.

The one-line run path keeps its user-facing fallback behavior: if update checking fails but a valid canonical command is already installed, it warns and runs that installed command. `llmgate update` is stricter: it only self-updates when the running executable is the canonical installed command, update failures exit non-zero, and the command never starts the setup wizard.

One-line run and update do not keep a separate versioned binary cache. They download release assets into temporary files, verify them, atomically replace the canonical command, write install metadata, and leave the canonical command as the only installed binary.

Install metadata is stored as user-local application state, not inside Unix command directories: `${XDG_STATE_HOME:-$HOME/.local/state}/llmgate/install.json` on Unix and `%LOCALAPPDATA%\llmgate\install.json` on Windows. The metadata points at the canonical command path and records the verified binary hash so ownership checks do not rely on PATH lookup or filename alone.

If install metadata exists but the canonical command's current hash does not match it, `llmgate` treats the command as externally modified and refuses to overwrite it automatically.
