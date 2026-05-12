package core

type SourceKind string

const (
	SourceUnknown              SourceKind = "unknown"
	SourceClaudeUserSettings   SourceKind = "claude_user_settings"
	SourceShellProfile         SourceKind = "shell_profile"
	SourceWindowsUserEnv       SourceKind = "windows_user_environment"
	SourceCurrentEnv           SourceKind = "current_environment"
	SourceVSCodeSettings       SourceKind = "vscode_settings"
	SourceCursorSettings       SourceKind = "cursor_settings"
	SourceProjectLocalSettings SourceKind = "project_local_settings"
	SourceProjectSettings      SourceKind = "project_settings"
	SourceUserInput            SourceKind = "user_input"
)

type SourceLabel struct {
	Kind   SourceKind
	Path   string
	Detail string
}

func (s SourceLabel) String() string {
	label := sourceKindLabel(s.Kind)
	if s.Path != "" {
		label += " (" + s.Path + ")"
	}
	if s.Detail != "" {
		label += ": " + s.Detail
	}
	return label
}

func sourceKindLabel(kind SourceKind) string {
	switch kind {
	case SourceClaudeUserSettings:
		return "Claude Code user settings"
	case SourceShellProfile:
		return "terminal shell profile"
	case SourceWindowsUserEnv:
		return "Windows user environment"
	case SourceCurrentEnv:
		return "current process environment"
	case SourceVSCodeSettings:
		return "VS Code settings"
	case SourceCursorSettings:
		return "Cursor settings"
	case SourceProjectLocalSettings:
		return "project local settings"
	case SourceProjectSettings:
		return "project settings"
	case SourceUserInput:
		return "user input"
	default:
		return "unknown source"
	}
}

type ConfigValue struct {
	Name   string
	Value  string
	Source SourceLabel
	Secret bool
}

type ResolvedValue struct {
	Name     string
	Value    string
	Source   SourceLabel
	Secret   bool
	Shadowed []ConfigValue
}

type ResolvedConfig struct {
	Name   string
	Values map[string]ResolvedValue
}

func (c ResolvedConfig) Get(name string) (ResolvedValue, bool) {
	value, ok := c.Values[name]
	return value, ok
}

type SetupValues struct {
	AuthToken   string
	BaseURL     string
	Model       string
	HaikuModel  string
	SonnetModel string
	OpusModel   string
}

func (v SetupValues) Map() map[string]string {
	values := map[string]string{
		VarAnthropicAuthToken:          v.AuthToken,
		VarAnthropicBaseURL:            v.BaseURL,
		VarAnthropicModel:              v.Model,
		VarAnthropicDefaultHaikuModel:  v.HaikuModel,
		VarAnthropicDefaultSonnetModel: v.SonnetModel,
		VarAnthropicDefaultOpusModel:   v.OpusModel,
	}
	for _, value := range BehaviorPrivacyDefaults {
		values[value.Name] = value.Default
	}
	return values
}

type WriteTargetKind string

const (
	WriteTargetClaudeUserSettings WriteTargetKind = "claude_user_settings"
	WriteTargetShellProfile       WriteTargetKind = "shell_profile"
	WriteTargetManualShell        WriteTargetKind = "manual_shell"
	WriteTargetWindowsUserEnv     WriteTargetKind = "windows_user_environment"
	WriteTargetVSCode             WriteTargetKind = "vscode"
	WriteTargetCursor             WriteTargetKind = "cursor"
)

type WriteTarget struct {
	Kind      WriteTargetKind
	Title     string
	Path      string
	Sensitive bool
	Writable  bool
	Exists    bool
}

func WriteTargetTitle(kind WriteTargetKind) string {
	switch kind {
	case WriteTargetClaudeUserSettings:
		return "Claude Code user settings"
	case WriteTargetShellProfile:
		return "terminal shell profile"
	case WriteTargetManualShell:
		return "manual shell setup"
	case WriteTargetWindowsUserEnv:
		return "Windows user environment"
	case WriteTargetVSCode:
		return "VS Code settings"
	case WriteTargetCursor:
		return "Cursor settings"
	default:
		return "unknown target"
	}
}
