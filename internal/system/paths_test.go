package system

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/r13v/llmgate/internal/core"
)

func TestDetectPathsDarwinZshAndVSCodeTarget(t *testing.T) {
	fileSystem := NewFakeFileSystem()
	fileSystem.AddDir("/Users/ada/Library/Application Support/Code/User")
	fileSystem.AddFile("/Users/ada/Library/Application Support/Code/User/settings.json", []byte("{}"))

	paths, err := DetectPaths(fileSystem, PathOptions{
		GOOS:       "darwin",
		HomeDir:    "/Users/ada",
		WorkingDir: "/Users/ada/work/llmgate-go",
		Shell:      "/bin/zsh",
	})
	if err != nil {
		t.Fatalf("DetectPaths returned error: %v", err)
	}

	if paths.ClaudeUserSettings != "/Users/ada/.claude/settings.json" {
		t.Fatalf("ClaudeUserSettings = %q", paths.ClaudeUserSettings)
	}
	if paths.ShellProfile.Kind != ShellZsh || paths.ShellProfile.Path != "/Users/ada/.zshrc" {
		t.Fatalf("ShellProfile = %#v", paths.ShellProfile)
	}
	if paths.ShellProfile.Exists {
		t.Fatalf("fresh zsh profile should be marked missing")
	}
	if paths.VSCode.SettingsPath != "/Users/ada/Library/Application Support/Code/User/settings.json" {
		t.Fatalf("VSCode.SettingsPath = %q", paths.VSCode.SettingsPath)
	}
	if !paths.VSCode.DirExists || !paths.VSCode.FileExists {
		t.Fatalf("VS Code target existence = dir %v file %v", paths.VSCode.DirExists, paths.VSCode.FileExists)
	}
	if paths.Cursor.DirExists {
		t.Fatalf("Cursor target should not be detected without existing config directory")
	}

	wantTargets := []core.WriteTargetKind{
		core.WriteTargetClaudeUserSettings,
		core.WriteTargetShellProfile,
		core.WriteTargetVSCode,
	}
	if got := targetKinds(paths.WriteTargets); !reflect.DeepEqual(got, wantTargets) {
		t.Fatalf("write targets = %v, want %v", got, wantTargets)
	}
}

func TestDetectPathsDarwinBashPrefersBashProfileWhenPresent(t *testing.T) {
	fileSystem := NewFakeFileSystem()
	fileSystem.AddFile("/Users/ada/.bash_profile", []byte("export EXISTING=1\n"))

	paths, err := DetectPaths(fileSystem, PathOptions{
		GOOS:       "darwin",
		HomeDir:    "/Users/ada",
		WorkingDir: "/Users/ada/work",
		Shell:      "/opt/homebrew/bin/bash",
	})
	if err != nil {
		t.Fatalf("DetectPaths returned error: %v", err)
	}

	if paths.ShellProfile.Path != "/Users/ada/.bash_profile" || !paths.ShellProfile.Exists {
		t.Fatalf("ShellProfile = %#v, want existing .bash_profile", paths.ShellProfile)
	}
}

func TestDetectPathsDarwinBashFallsBackToBashrc(t *testing.T) {
	paths, err := DetectPaths(NewFakeFileSystem(), PathOptions{
		GOOS:       "darwin",
		HomeDir:    "/Users/ada",
		WorkingDir: "/Users/ada/work",
		Shell:      "/bin/bash",
	})
	if err != nil {
		t.Fatalf("DetectPaths returned error: %v", err)
	}

	if paths.ShellProfile.Path != "/Users/ada/.bashrc" || paths.ShellProfile.Exists {
		t.Fatalf("ShellProfile = %#v, want missing .bashrc fallback", paths.ShellProfile)
	}
}

func TestDetectPathsLinuxFishAndCursorTarget(t *testing.T) {
	fileSystem := NewFakeFileSystem()
	fileSystem.AddDir("/home/ada/.config/Cursor/User")

	paths, err := DetectPaths(fileSystem, PathOptions{
		GOOS:       "linux",
		HomeDir:    "/home/ada",
		WorkingDir: "/home/ada/project",
		Shell:      "/usr/bin/fish",
	})
	if err != nil {
		t.Fatalf("DetectPaths returned error: %v", err)
	}

	if paths.ShellProfile.Kind != ShellFish || paths.ShellProfile.Path != "/home/ada/.config/fish/config.fish" {
		t.Fatalf("ShellProfile = %#v", paths.ShellProfile)
	}
	if paths.Cursor.SettingsPath != "/home/ada/.config/Cursor/User/settings.json" || !paths.Cursor.DirExists {
		t.Fatalf("Cursor = %#v", paths.Cursor)
	}
	if paths.Cursor.FileExists {
		t.Fatalf("Cursor settings file should be marked missing when only the directory exists")
	}
	if !hasTarget(paths.WriteTargets, core.WriteTargetCursor) {
		t.Fatalf("Cursor write target missing from %v", targetKinds(paths.WriteTargets))
	}
}

func TestDetectPathsLinuxUnknownShellUsesManualTarget(t *testing.T) {
	paths, err := DetectPaths(NewFakeFileSystem(), PathOptions{
		GOOS:       "linux",
		HomeDir:    "/home/ada",
		WorkingDir: "/home/ada/project",
		Shell:      "/usr/bin/tcsh",
	})
	if err != nil {
		t.Fatalf("DetectPaths returned error: %v", err)
	}

	if paths.ShellProfile.Detected {
		t.Fatalf("unexpected detected shell profile: %#v", paths.ShellProfile)
	}
	if got := targetKinds(paths.WriteTargets); !reflect.DeepEqual(got, []core.WriteTargetKind{
		core.WriteTargetClaudeUserSettings,
		core.WriteTargetManualShell,
	}) {
		t.Fatalf("write targets = %v", got)
	}
}

func TestDetectPathsWindowsUsesAppDataAndUserEnvironment(t *testing.T) {
	fileSystem := NewFakeFileSystem()
	fileSystem.AddDir(`C:\Users\Ada\AppData\Roaming\Code\User`)
	fileSystem.AddFile(`C:\Users\Ada\AppData\Roaming\Code\User\settings.json`, []byte("{}"))

	paths, err := DetectPaths(fileSystem, PathOptions{
		GOOS:       "windows",
		HomeDir:    `C:\Users\Ada`,
		WorkingDir: `D:\repo`,
		Shell:      `/usr/bin/zsh`,
		AppData:    `C:\Users\Ada\AppData\Roaming`,
	})
	if err != nil {
		t.Fatalf("DetectPaths returned error: %v", err)
	}

	if paths.ClaudeUserSettings != `C:\Users\Ada\.claude\settings.json` {
		t.Fatalf("ClaudeUserSettings = %q", paths.ClaudeUserSettings)
	}
	if paths.ProjectLocalSettings != `D:\repo\.claude\settings.local.json` {
		t.Fatalf("ProjectLocalSettings = %q", paths.ProjectLocalSettings)
	}
	if paths.ShellProfile.Detected {
		t.Fatalf("windows should not detect shell profile: %#v", paths.ShellProfile)
	}
	if !paths.WindowsUserEnv {
		t.Fatalf("WindowsUserEnv = false, want true")
	}
	if paths.VSCode.SettingsPath != `C:\Users\Ada\AppData\Roaming\Code\User\settings.json` {
		t.Fatalf("VSCode.SettingsPath = %q", paths.VSCode.SettingsPath)
	}
	if got := targetKinds(paths.WriteTargets); !reflect.DeepEqual(got, []core.WriteTargetKind{
		core.WriteTargetClaudeUserSettings,
		core.WriteTargetWindowsUserEnv,
		core.WriteTargetVSCode,
	}) {
		t.Fatalf("write targets = %v", got)
	}
}

func TestDetectPathsWindowsFallsBackToRoamingAppData(t *testing.T) {
	fileSystem := NewFakeFileSystem()
	fileSystem.AddDir(`C:\Users\Ada\AppData\Roaming\Cursor\User`)

	paths, err := DetectPaths(fileSystem, PathOptions{
		GOOS:       "windows",
		HomeDir:    `C:\Users\Ada`,
		WorkingDir: `C:\Users\Ada\project`,
	})
	if err != nil {
		t.Fatalf("DetectPaths returned error: %v", err)
	}

	if paths.Cursor.SettingsPath != `C:\Users\Ada\AppData\Roaming\Cursor\User\settings.json` {
		t.Fatalf("Cursor.SettingsPath = %q", paths.Cursor.SettingsPath)
	}
	if !hasTarget(paths.WriteTargets, core.WriteTargetCursor) {
		t.Fatalf("Cursor write target missing from %v", targetKinds(paths.WriteTargets))
	}
}

func TestSystemPathOptionsUsesInjectedPlatformAndEnvironment(t *testing.T) {
	sys := System{
		Env: FakeEnvironment{Values: map[string]string{
			"SHELL":   "/bin/bash",
			"APPDATA": `C:\Roaming`,
		}},
		Platform: FakePlatform{
			TargetOS: "windows",
			Home:     `C:\Users\Ada`,
			WorkDir:  `C:\repo`,
		},
	}

	options, err := sys.PathOptions()
	if err != nil {
		t.Fatalf("PathOptions returned error: %v", err)
	}
	if options.GOOS != "windows" || options.HomeDir != `C:\Users\Ada` || options.WorkingDir != `C:\repo` {
		t.Fatalf("PathOptions = %#v", options)
	}
	if options.Shell != "/bin/bash" || options.AppData != `C:\Roaming` {
		t.Fatalf("PathOptions env fields = %#v", options)
	}
}

func TestManagedEnvironmentFiltersManagedNames(t *testing.T) {
	values := ManagedEnvironment(FakeEnvironment{Values: map[string]string{
		core.VarAnthropicAuthToken: "secret",
		core.VarAnthropicBaseURL:   "https://gateway.example.com",
		"UNRELATED":                "ignored",
	}})

	if len(values) != 2 {
		t.Fatalf("len(values) = %d, want 2: %#v", len(values), values)
	}
	if values[core.VarAnthropicAuthToken] != "secret" || values[core.VarAnthropicBaseURL] == "" {
		t.Fatalf("managed values = %#v", values)
	}
}

func TestClaudeVersionUsesRunner(t *testing.T) {
	runner := &FakeCommandRunner{
		Result: CommandResult{Stdout: "Claude Code 1.2.3\n"},
	}

	version, err := ClaudeVersion(context.Background(), runner)
	if err != nil {
		t.Fatalf("ClaudeVersion returned error: %v", err)
	}
	if version != "Claude Code 1.2.3" {
		t.Fatalf("version = %q", version)
	}
	if len(runner.Calls) != 1 || runner.Calls[0].Name != "claude" || !reflect.DeepEqual(runner.Calls[0].Args, []string{"--version"}) {
		t.Fatalf("runner calls = %#v", runner.Calls)
	}
}

func TestClaudeVersionReturnsStderrOnFailure(t *testing.T) {
	runner := &FakeCommandRunner{
		Result: CommandResult{Stderr: "claude not found\n", ExitCode: 127},
		Err:    errors.New("exit status 127"),
	}

	output, err := ClaudeVersion(context.Background(), runner)
	if err == nil {
		t.Fatalf("ClaudeVersion returned nil error")
	}
	if output != "claude not found" {
		t.Fatalf("output = %q", output)
	}
}

func targetKinds(targets []core.WriteTarget) []core.WriteTargetKind {
	kinds := make([]core.WriteTargetKind, 0, len(targets))
	for _, target := range targets {
		kinds = append(kinds, target.Kind)
	}
	return kinds
}

func hasTarget(targets []core.WriteTarget, kind core.WriteTargetKind) bool {
	for _, target := range targets {
		if target.Kind == kind {
			return true
		}
	}
	return false
}
