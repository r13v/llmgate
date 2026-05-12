package system

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"runtime"
	"strings"

	"github.com/r13v/llmgate/internal/core"
)

type ShellKind string

const (
	ShellNone    ShellKind = ""
	ShellZsh     ShellKind = "zsh"
	ShellBash    ShellKind = "bash"
	ShellFish    ShellKind = "fish"
	ShellUnknown ShellKind = "unknown"
)

type PathOptions struct {
	GOOS       string
	HomeDir    string
	WorkingDir string
	Shell      string
	AppData    string
}

type ShellProfile struct {
	Kind     ShellKind
	Path     string
	Detected bool
	Exists   bool
}

type IDETarget struct {
	Kind         core.WriteTargetKind
	Title        string
	SettingsDir  string
	SettingsPath string
	DirExists    bool
	FileExists   bool
}

type DiscoveredPaths struct {
	GOOS                 string
	HomeDir              string
	WorkingDir           string
	ClaudeUserSettings   string
	ClaudeUserExists     bool
	ShellProfile         ShellProfile
	WindowsUserEnv       bool
	VSCode               IDETarget
	Cursor               IDETarget
	ProjectLocalSettings string
	ProjectLocalExists   bool
	ProjectSettings      string
	ProjectExists        bool
	WriteTargets         []core.WriteTarget
}

func DetectPaths(fileSystem FileSystem, options PathOptions) (DiscoveredPaths, error) {
	if fileSystem == nil {
		fileSystem = RealFileSystem{}
	}

	targetOS := effectiveGOOS(options.GOOS)
	if options.HomeDir == "" {
		return DiscoveredPaths{}, fmt.Errorf("home directory is required")
	}
	if options.WorkingDir == "" {
		return DiscoveredPaths{}, fmt.Errorf("working directory is required")
	}

	claudeUserSettings := joinPath(targetOS, options.HomeDir, ".claude", "settings.json")
	claudeExists, _, err := statPath(fileSystem, claudeUserSettings)
	if err != nil {
		return DiscoveredPaths{}, err
	}

	projectLocalSettings := joinPath(targetOS, options.WorkingDir, ".claude", "settings.local.json")
	projectLocalExists, _, err := statPath(fileSystem, projectLocalSettings)
	if err != nil {
		return DiscoveredPaths{}, err
	}

	projectSettings := joinPath(targetOS, options.WorkingDir, ".claude", "settings.json")
	projectExists, _, err := statPath(fileSystem, projectSettings)
	if err != nil {
		return DiscoveredPaths{}, err
	}

	shellProfile, err := detectShellProfile(fileSystem, targetOS, options.HomeDir, options.Shell)
	if err != nil {
		return DiscoveredPaths{}, err
	}

	vsCode, err := detectIDETarget(fileSystem, targetOS, options.HomeDir, options.AppData, core.WriteTargetVSCode)
	if err != nil {
		return DiscoveredPaths{}, err
	}
	cursor, err := detectIDETarget(fileSystem, targetOS, options.HomeDir, options.AppData, core.WriteTargetCursor)
	if err != nil {
		return DiscoveredPaths{}, err
	}

	discovered := DiscoveredPaths{
		GOOS:                 targetOS,
		HomeDir:              options.HomeDir,
		WorkingDir:           options.WorkingDir,
		ClaudeUserSettings:   claudeUserSettings,
		ClaudeUserExists:     claudeExists,
		ShellProfile:         shellProfile,
		WindowsUserEnv:       targetOS == "windows",
		VSCode:               vsCode,
		Cursor:               cursor,
		ProjectLocalSettings: projectLocalSettings,
		ProjectLocalExists:   projectLocalExists,
		ProjectSettings:      projectSettings,
		ProjectExists:        projectExists,
	}
	discovered.WriteTargets = buildWriteTargets(discovered)
	return discovered, nil
}

func buildWriteTargets(paths DiscoveredPaths) []core.WriteTarget {
	targets := []core.WriteTarget{
		{
			Kind:      core.WriteTargetClaudeUserSettings,
			Title:     core.WriteTargetTitle(core.WriteTargetClaudeUserSettings),
			Path:      paths.ClaudeUserSettings,
			Sensitive: true,
			Writable:  true,
			Exists:    paths.ClaudeUserExists,
		},
	}

	switch {
	case paths.WindowsUserEnv:
		targets = append(targets, core.WriteTarget{
			Kind:      core.WriteTargetWindowsUserEnv,
			Title:     core.WriteTargetTitle(core.WriteTargetWindowsUserEnv),
			Sensitive: true,
			Writable:  true,
			Exists:    true,
		})
	case paths.ShellProfile.Detected:
		targets = append(targets, core.WriteTarget{
			Kind:      core.WriteTargetShellProfile,
			Title:     core.WriteTargetTitle(core.WriteTargetShellProfile),
			Path:      paths.ShellProfile.Path,
			Sensitive: true,
			Writable:  true,
			Exists:    paths.ShellProfile.Exists,
		})
	default:
		targets = append(targets, core.WriteTarget{
			Kind:      core.WriteTargetManualShell,
			Title:     core.WriteTargetTitle(core.WriteTargetManualShell),
			Sensitive: true,
			Writable:  false,
			Exists:    false,
		})
	}

	if paths.VSCode.DirExists {
		targets = append(targets, ideWriteTarget(paths.VSCode))
	}
	if paths.Cursor.DirExists {
		targets = append(targets, ideWriteTarget(paths.Cursor))
	}
	return targets
}

func ideWriteTarget(target IDETarget) core.WriteTarget {
	return core.WriteTarget{
		Kind:      target.Kind,
		Title:     target.Title,
		Path:      target.SettingsPath,
		Sensitive: true,
		Writable:  true,
		Exists:    target.FileExists,
	}
}

func detectShellProfile(fileSystem FileSystem, targetOS, homeDir, shell string) (ShellProfile, error) {
	if targetOS == "windows" {
		return ShellProfile{}, nil
	}
	if targetOS != "darwin" && targetOS != "linux" {
		return ShellProfile{Kind: ShellUnknown}, nil
	}

	kind := shellKind(shell)
	switch kind {
	case ShellZsh:
		return shellProfile(fileSystem, targetOS, homeDir, kind, ".zshrc")
	case ShellFish:
		return shellProfile(fileSystem, targetOS, homeDir, kind, ".config", "fish", "config.fish")
	case ShellBash:
		if targetOS == "darwin" {
			bashProfilePath := joinPath(targetOS, homeDir, ".bash_profile")
			exists, isDir, err := statPath(fileSystem, bashProfilePath)
			if err != nil {
				return ShellProfile{}, err
			}
			if exists && !isDir {
				return ShellProfile{Kind: kind, Path: bashProfilePath, Detected: true, Exists: true}, nil
			}
		}
		return shellProfile(fileSystem, targetOS, homeDir, kind, ".bashrc")
	case ShellNone:
		return ShellProfile{Kind: ShellNone}, nil
	default:
		return ShellProfile{Kind: ShellUnknown}, nil
	}
}

func shellProfile(fileSystem FileSystem, targetOS, homeDir string, kind ShellKind, elements ...string) (ShellProfile, error) {
	profilePath := joinPath(append([]string{targetOS, homeDir}, elements...)...)
	exists, _, err := statPath(fileSystem, profilePath)
	if err != nil {
		return ShellProfile{}, err
	}
	return ShellProfile{
		Kind:     kind,
		Path:     profilePath,
		Detected: true,
		Exists:   exists,
	}, nil
}

func detectIDETarget(fileSystem FileSystem, targetOS, homeDir, appData string, kind core.WriteTargetKind) (IDETarget, error) {
	settingsDir, settingsPath := idePaths(targetOS, homeDir, appData, kind)
	target := IDETarget{
		Kind:         kind,
		Title:        core.WriteTargetTitle(kind),
		SettingsDir:  settingsDir,
		SettingsPath: settingsPath,
	}
	if settingsDir == "" {
		return target, nil
	}

	dirExists, isDir, err := statPath(fileSystem, settingsDir)
	if err != nil {
		return IDETarget{}, err
	}
	target.DirExists = dirExists && isDir
	if !target.DirExists {
		return target, nil
	}

	fileExists, _, err := statPath(fileSystem, settingsPath)
	if err != nil {
		return IDETarget{}, err
	}
	target.FileExists = fileExists
	return target, nil
}

func idePaths(targetOS, homeDir, appData string, kind core.WriteTargetKind) (string, string) {
	var appName string
	switch kind {
	case core.WriteTargetVSCode:
		appName = "Code"
	case core.WriteTargetCursor:
		appName = "Cursor"
	default:
		return "", ""
	}

	switch targetOS {
	case "darwin":
		dir := joinPath(targetOS, homeDir, "Library", "Application Support", appName, "User")
		return dir, joinPath(targetOS, dir, "settings.json")
	case "linux":
		dir := joinPath(targetOS, homeDir, ".config", appName, "User")
		return dir, joinPath(targetOS, dir, "settings.json")
	case "windows":
		root := appData
		if root == "" {
			root = joinPath(targetOS, homeDir, "AppData", "Roaming")
		}
		dir := joinPath(targetOS, root, appName, "User")
		return dir, joinPath(targetOS, dir, "settings.json")
	default:
		return "", ""
	}
}

func shellKind(shell string) ShellKind {
	shell = strings.TrimSpace(shell)
	if shell == "" {
		return ShellNone
	}
	shell = strings.ReplaceAll(shell, `\`, "/")
	if index := strings.LastIndex(shell, "/"); index >= 0 {
		shell = shell[index+1:]
	}
	shell = strings.TrimPrefix(shell, "-")
	switch shell {
	case "zsh":
		return ShellZsh
	case "bash":
		return ShellBash
	case "fish":
		return ShellFish
	default:
		return ShellUnknown
	}
}

func statPath(fileSystem FileSystem, name string) (bool, bool, error) {
	info, err := fileSystem.Stat(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, false, nil
		}
		return false, false, err
	}
	return true, info.IsDir(), nil
}

func effectiveGOOS(targetOS string) string {
	if targetOS == "" {
		return runtime.GOOS
	}
	return strings.ToLower(targetOS)
}

func joinPath(elements ...string) string {
	if len(elements) == 0 {
		return ""
	}
	targetOS := elements[0]
	parts := elements[1:]
	if targetOS == "windows" {
		return joinWindowsPath(parts...)
	}
	return path.Join(parts...)
}

func joinWindowsPath(elements ...string) string {
	var output string
	for _, element := range elements {
		if element == "" {
			continue
		}
		element = strings.ReplaceAll(element, "/", `\`)
		if output == "" {
			output = strings.TrimRight(element, `\`)
			continue
		}
		output += `\` + strings.Trim(element, `\`)
	}
	return output
}
