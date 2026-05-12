package config

import (
	"fmt"

	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/settings"
	"github.com/r13v/llmgate/internal/shell"
	"github.com/r13v/llmgate/internal/system"
)

type ReadResult struct {
	Approved     bool
	Paths        system.DiscoveredPaths
	Sources      []Source
	SourceIssues []SourceIssue
	WriteTargets []core.WriteTarget
}

func Read(sys system.System, approved bool) (ReadResult, error) {
	if !approved {
		return ReadResult{}, nil
	}

	fileSystem := sys.FS
	if fileSystem == nil {
		fileSystem = system.RealFileSystem{}
	}

	paths, err := sys.DetectPaths()
	if err != nil {
		return ReadResult{}, err
	}

	result := ReadResult{
		Approved:     true,
		Paths:        paths,
		WriteTargets: append([]core.WriteTarget(nil), paths.WriteTargets...),
	}

	readClaudeUser(fileSystem, paths, &result)
	readPersistedEnvironment(sys, fileSystem, paths, &result)
	readCurrentEnvironment(sys.Env, &result)
	readIDE(fileSystem, paths.VSCode, core.SourceVSCodeSettings, &result)
	readIDE(fileSystem, paths.Cursor, core.SourceCursorSettings, &result)
	readProject(fileSystem, paths.ProjectLocalSettings, paths.ProjectLocalExists, core.SourceProjectLocalSettings, &result)
	readProject(fileSystem, paths.ProjectSettings, paths.ProjectExists, core.SourceProjectSettings, &result)

	return result, nil
}

func readClaudeUser(fileSystem system.FileSystem, paths system.DiscoveredPaths, result *ReadResult) {
	label := core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: paths.ClaudeUserSettings}
	if !paths.ClaudeUserExists {
		return
	}

	data, err := fileSystem.ReadFile(paths.ClaudeUserSettings)
	if err != nil {
		result.SourceIssues = append(result.SourceIssues, sourceIssue(
			SourceIssueReadError,
			core.StatusFAIL,
			label,
			fmt.Sprintf("read Claude Code user settings: %s", err),
		))
		return
	}

	parsed, err := settings.ParseClaude(data)
	if err != nil {
		result.SourceIssues = append(result.SourceIssues, sourceIssue(
			SourceIssueMalformed,
			core.StatusFAIL,
			label,
			err.Error(),
		))
		return
	}
	result.Sources = append(result.Sources, source(label, parsed.Env))
}

func readPersistedEnvironment(sys system.System, fileSystem system.FileSystem, paths system.DiscoveredPaths, result *ReadResult) {
	if paths.WindowsUserEnv {
		readWindowsUserEnvironment(sys.WindowsEnv, result)
		return
	}
	if !paths.ShellProfile.Detected || !paths.ShellProfile.Exists {
		return
	}

	label := core.SourceLabel{Kind: core.SourceShellProfile, Path: paths.ShellProfile.Path}
	data, err := fileSystem.ReadFile(paths.ShellProfile.Path)
	if err != nil {
		result.SourceIssues = append(result.SourceIssues, sourceIssue(
			SourceIssueReadError,
			core.StatusFAIL,
			label,
			fmt.Sprintf("read shell profile: %s", err),
		))
		return
	}

	syntax, err := syntaxForShell(paths.ShellProfile.Kind)
	if err != nil {
		result.SourceIssues = append(result.SourceIssues, sourceIssue(
			SourceIssueReadError,
			core.StatusFAIL,
			label,
			err.Error(),
		))
		return
	}

	profile, err := shell.ParseProfile(data, syntax)
	if err != nil {
		result.SourceIssues = append(result.SourceIssues, sourceIssue(
			SourceIssueReadError,
			core.StatusFAIL,
			label,
			err.Error(),
		))
		return
	}

	values := make(map[string]string, len(profile.Values))
	for name, value := range profile.Values {
		values[name] = value.Value
	}
	src := source(label, values)
	src.ShellIssues = append(src.ShellIssues, profile.Issues...)
	result.Sources = append(result.Sources, src)
}

func readWindowsUserEnvironment(windowsEnv system.WindowsUserEnvironment, result *ReadResult) {
	label := core.SourceLabel{Kind: core.SourceWindowsUserEnv}
	if windowsEnv == nil {
		windowsEnv = system.NewWindowsUserEnvironment()
	}
	values, err := windowsEnv.Snapshot(core.AllManagedNames())
	if err != nil {
		result.SourceIssues = append(result.SourceIssues, sourceIssue(
			SourceIssueReadError,
			core.StatusFAIL,
			label,
			fmt.Sprintf("read Windows user environment: %s", err),
		))
		return
	}
	result.Sources = append(result.Sources, source(label, values))
}

func readCurrentEnvironment(env system.ProcessEnvironment, result *ReadResult) {
	label := core.SourceLabel{Kind: core.SourceCurrentEnv}
	values := system.ManagedEnvironment(env)
	if len(values) == 0 {
		return
	}
	result.Sources = append(result.Sources, source(label, values))
}

func readIDE(fileSystem system.FileSystem, target system.IDETarget, kind core.SourceKind, result *ReadResult) {
	if !target.FileExists {
		return
	}

	label := core.SourceLabel{Kind: kind, Path: target.SettingsPath}
	data, err := fileSystem.ReadFile(target.SettingsPath)
	if err != nil {
		result.SourceIssues = append(result.SourceIssues, sourceIssue(
			SourceIssueReadError,
			core.StatusWARN,
			label,
			fmt.Sprintf("read %s: %s", target.Title, err),
		))
		return
	}

	parsed, err := settings.ParseIDE(data)
	if err != nil {
		result.SourceIssues = append(result.SourceIssues, sourceIssue(
			SourceIssueMalformed,
			core.StatusWARN,
			label,
			err.Error(),
		))
		return
	}

	src := source(label, parsed.Environment)
	if parsed.HasSelectedModel {
		model := selectedModelValue(label, parsed.SelectedModel)
		src.SelectedModel = &model
	}
	result.Sources = append(result.Sources, src)
}

func readProject(fileSystem system.FileSystem, path string, exists bool, kind core.SourceKind, result *ReadResult) {
	if !exists {
		return
	}

	label := core.SourceLabel{Kind: kind, Path: path}
	data, err := fileSystem.ReadFile(path)
	if err != nil {
		result.SourceIssues = append(result.SourceIssues, sourceIssue(
			SourceIssueReadError,
			core.StatusWARN,
			label,
			fmt.Sprintf("read project settings: %s", err),
		))
		return
	}

	parsed, err := settings.ParseClaude(data)
	if err != nil {
		result.SourceIssues = append(result.SourceIssues, sourceIssue(
			SourceIssueMalformed,
			core.StatusWARN,
			label,
			err.Error(),
		))
		return
	}
	result.Sources = append(result.Sources, source(label, parsed.Env))
}

func syntaxForShell(kind system.ShellKind) (shell.Syntax, error) {
	switch kind {
	case system.ShellZsh, system.ShellBash:
		return shell.SyntaxPOSIX, nil
	case system.ShellFish:
		return shell.SyntaxFish, nil
	default:
		return "", fmt.Errorf("unsupported shell profile kind %q", kind)
	}
}
