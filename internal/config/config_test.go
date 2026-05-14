package config

import (
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/system"
)

func TestReadDeclineDoesNotTouchLocalSystem(t *testing.T) {
	result, err := Read(system.System{
		FS:         panicFileSystem{},
		Env:        panicEnvironment{},
		Platform:   panicPlatform{},
		WindowsEnv: panicWindowsEnvironment{},
	}, false)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if result.Approved {
		t.Fatalf("Approved = true, want false")
	}
	if len(result.Sources) != 0 || len(result.SourceIssues) != 0 || len(result.WriteTargets) != 0 {
		t.Fatalf("declined read touched state: %#v", result)
	}
}

func TestReadAndResolveUnixSourcePrecedence(t *testing.T) {
	fileSystem := newTestFileSystem()
	fileSystem.addFile("/home/ada/.claude/settings.json", []byte(`{
		"env": {
			"ANTHROPIC_AUTH_TOKEN": "sk-claude123456",
			"ANTHROPIC_BASE_URL": "https://settings.example.com",
			"ANTHROPIC_MODEL": "claude-from-settings"
		}
	}`))
	fileSystem.addFile("/home/ada/.zshrc", []byte(strings.Join([]string{
		"export ANTHROPIC_BASE_URL='https://shell.example.com'",
		"export ANTHROPIC_DEFAULT_OPUS_MODEL='opus-from-shell'",
		"",
	}, "\n")))

	read, err := Read(testSystem(fileSystem, "linux", "/home/ada", "/home/ada/project", map[string]string{
		"SHELL":              "/bin/zsh",
		"ANTHROPIC_BASE_URL": "https://settings.example.com",
		"ANTHROPIC_MODEL":    "claude-from-current-env",
	}), true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(read.SourceIssues) != 0 {
		t.Fatalf("SourceIssues = %#v, want none", read.SourceIssues)
	}

	resolved := Resolve(read)
	assertResolved(t, resolved.Persisted, core.VarAnthropicBaseURL, "https://shell.example.com", core.SourceShellProfile)
	assertResolved(t, resolved.Current, core.VarAnthropicModel, "claude-from-current-env", core.SourceCurrentEnv)

	currentBase := assertResolved(t, resolved.Current, core.VarAnthropicBaseURL, "https://settings.example.com", core.SourceClaudeUserSettings)
	if len(currentBase.Shadowed) != 0 {
		t.Fatalf("current equal env value should keep settings source without shadowing: %#v", currentBase)
	}

	persistedBase, ok := resolved.Persisted.Get(core.VarAnthropicBaseURL)
	if !ok || len(persistedBase.Shadowed) != 1 || persistedBase.Shadowed[0].Source.Kind != core.SourceClaudeUserSettings {
		t.Fatalf("persisted shadowed base value = %#v, want Claude settings shadowed by shell", persistedBase)
	}
	assertRuntimeDifference(t, resolved.Runtime, DifferenceCurrentDiffers, core.VarAnthropicBaseURL)
	assertRuntimeDifference(t, resolved.Runtime, DifferencePersistedOnly, core.VarAnthropicDefaultOpusModel)
}

func TestResolveWithOptionsUsesCurrentProcessEnvironmentByDefault(t *testing.T) {
	fileSystem := newTestFileSystem()
	fileSystem.addFile("/home/ada/.zshrc", []byte(strings.Join([]string{
		"export ANTHROPIC_BASE_URL='https://new-session.example.com'",
		"",
	}, "\n")))

	read, err := Read(testSystem(fileSystem, "linux", "/home/ada", "/home/ada/project", map[string]string{
		"SHELL":              "/bin/zsh",
		"ANTHROPIC_BASE_URL": "https://stale-process.example.com",
	}), true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	for name, resolved := range map[string]Resolution{
		"Resolve":        Resolve(read),
		"empty options":  ResolveWithOptions(read, ResolveOptions{}),
		"process option": ResolveWithOptions(read, ResolveOptions{CurrentMode: CurrentModeProcessEnvironment}),
	} {
		t.Run(name, func(t *testing.T) {
			if resolved.Current.Name != "current environment" {
				t.Fatalf("Current.Name = %q, want current environment", resolved.Current.Name)
			}
			assertResolved(t, resolved.Current, core.VarAnthropicBaseURL, "https://stale-process.example.com", core.SourceCurrentEnv)
			assertRuntimeDifference(t, resolved.Runtime, DifferenceCurrentDiffers, core.VarAnthropicBaseURL)
		})
	}
}

func TestResolveWithOptionsNewSessionUsesPersistedGlobalSources(t *testing.T) {
	fileSystem := newTestFileSystem()
	fileSystem.addFile("/home/ada/.zshrc", []byte(strings.Join([]string{
		"export ANTHROPIC_BASE_URL='https://new-session.example.com'",
		"export ANTHROPIC_MODEL='claude-new-session'",
		"",
	}, "\n")))
	fileSystem.addDir("/home/ada/.config/Code/User")
	fileSystem.addFile("/home/ada/.config/Code/User/settings.json", []byte(`{
		"claudeCode.selectedModel": "claude-ide-stale",
		"claudeCode.environmentVariables": [
			{ "name": "ANTHROPIC_BASE_URL", "value": "https://new-session.example.com" }
		]
	}`))
	fileSystem.addFile("/home/ada/project/.claude/settings.local.json", []byte(`{
		"env": {
			"ANTHROPIC_BASE_URL": "https://project.example.com"
		}
	}`))

	read, err := Read(testSystem(fileSystem, "linux", "/home/ada", "/home/ada/project", map[string]string{
		"SHELL":              "/bin/zsh",
		"ANTHROPIC_BASE_URL": "https://stale-process.example.com",
		"ANTHROPIC_MODEL":    "claude-stale-process",
	}), true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	resolved := ResolveWithOptions(read, ResolveOptions{CurrentMode: CurrentModeNewSession})
	if resolved.Current.Name != "new terminal session" {
		t.Fatalf("Current.Name = %q, want new terminal session", resolved.Current.Name)
	}
	assertResolved(t, resolved.Current, core.VarAnthropicBaseURL, "https://new-session.example.com", core.SourceShellProfile)
	assertResolved(t, resolved.Current, core.VarAnthropicModel, "claude-new-session", core.SourceShellProfile)
	if len(resolved.Runtime) != 0 {
		t.Fatalf("Runtime = %#v, want no differences for new-session current context", resolved.Runtime)
	}
	if hasSideDifference(resolved.IDEDrift, core.VarAnthropicBaseURL, core.SourceVSCodeSettings) {
		t.Fatalf("IDE value matching new session should not be reported as drift: %#v", resolved.IDEDrift)
	}
	projectDifference := assertSideDifference(t, resolved.ProjectOverrides, DifferenceProjectOverride, core.VarAnthropicBaseURL, core.SourceProjectLocalSettings)
	if projectDifference.ComparedAgainst != "new terminal session" {
		t.Fatalf("project ComparedAgainst = %q, want new terminal session", projectDifference.ComparedAgainst)
	}
	ideDifference := assertSideDifference(t, resolved.IDEDrift, DifferenceIDEDrift, core.VarAnthropicModel, core.SourceVSCodeSettings)
	if ideDifference.ComparedAgainst != "new terminal session" {
		t.Fatalf("IDE ComparedAgainst = %q, want new terminal session", ideDifference.ComparedAgainst)
	}
}

func TestResolveDetectsPersistedConflictsShellIssuesAndCurrentOnlyValues(t *testing.T) {
	fileSystem := newTestFileSystem()
	fileSystem.addFile("/home/ada/.claude/settings.json", []byte(`{
		"env": {
			"ANTHROPIC_BASE_URL": "https://settings.example.com"
		}
	}`))
	fileSystem.addFile("/home/ada/.bashrc", []byte(strings.Join([]string{
		"export ANTHROPIC_BASE_URL='https://first-shell.example.com'",
		"export ANTHROPIC_BASE_URL='https://second-shell.example.com'",
		"export ANTHROPIC_DEFAULT_HAIKU_MODEL=$(choose-haiku)",
		"",
	}, "\n")))

	read, err := Read(testSystem(fileSystem, "linux", "/home/ada", "/home/ada/project", map[string]string{
		"SHELL":                          "/bin/bash",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "sonnet-from-current-env",
	}), true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	resolved := Resolve(read)

	assertResolved(t, resolved.Persisted, core.VarAnthropicBaseURL, "https://second-shell.example.com", core.SourceShellProfile)
	assertConflict(t, resolved.Conflicts, ConflictPersistedValue, core.VarAnthropicBaseURL)
	assertConflict(t, resolved.Conflicts, ConflictShellDuplicate, core.VarAnthropicBaseURL)
	assertConflict(t, resolved.Conflicts, ConflictShellDynamic, core.VarAnthropicDefaultHaikuModel)
	assertRuntimeDifference(t, resolved.Runtime, DifferenceCurrentOnly, core.VarAnthropicDefaultSonnetModel)
}

func TestResolveWindowsUserEnvironmentWinsPersistedResolution(t *testing.T) {
	fileSystem := newTestFileSystem()
	fileSystem.addFile(`C:\Users\Ada\.claude\settings.json`, []byte(`{
		"env": {
			"ANTHROPIC_BASE_URL": "https://settings.example.com"
		}
	}`))
	windowsEnv := &testWindowsEnvironment{values: map[string]string{
		"ANTHROPIC_BASE_URL": "https://windows-env.example.com",
	}}

	read, err := Read(system.System{
		FS: fileSystem,
		Env: &testEnvironment{values: map[string]string{
			"APPDATA": `C:\Users\Ada\AppData\Roaming`,
		}},
		Platform:   testPlatform{targetOS: "windows", home: `C:\Users\Ada`, work: `D:\repo`},
		WindowsEnv: windowsEnv,
	}, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	resolved := Resolve(read)
	assertResolved(t, resolved.Persisted, core.VarAnthropicBaseURL, "https://windows-env.example.com", core.SourceWindowsUserEnv)
	if windowsEnv.snapshots != 1 {
		t.Fatalf("Windows Snapshot calls = %d, want 1", windowsEnv.snapshots)
	}
}

func TestResolveWithOptionsNewSessionUsesWindowsUserEnvironment(t *testing.T) {
	fileSystem := newTestFileSystem()
	fileSystem.addFile(`C:\Users\Ada\.claude\settings.json`, []byte(`{
		"env": {
			"ANTHROPIC_BASE_URL": "https://settings.example.com"
		}
	}`))
	windowsEnv := &testWindowsEnvironment{values: map[string]string{
		"ANTHROPIC_BASE_URL": "https://windows-env.example.com",
		"ANTHROPIC_MODEL":    "claude-new-session",
	}}

	read, err := Read(system.System{
		FS: fileSystem,
		Env: &testEnvironment{values: map[string]string{
			"APPDATA":            `C:\Users\Ada\AppData\Roaming`,
			"ANTHROPIC_BASE_URL": "https://stale-process.example.com",
			"ANTHROPIC_MODEL":    "claude-stale-process",
		}},
		Platform:   testPlatform{targetOS: "windows", home: `C:\Users\Ada`, work: `D:\repo`},
		WindowsEnv: windowsEnv,
	}, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	resolved := ResolveWithOptions(read, ResolveOptions{CurrentMode: CurrentModeNewSession})
	if resolved.Current.Name != "new terminal session" {
		t.Fatalf("Current.Name = %q, want new terminal session", resolved.Current.Name)
	}
	assertResolved(t, resolved.Current, core.VarAnthropicBaseURL, "https://windows-env.example.com", core.SourceWindowsUserEnv)
	assertResolved(t, resolved.Current, core.VarAnthropicModel, "claude-new-session", core.SourceWindowsUserEnv)
	if len(resolved.Runtime) != 0 {
		t.Fatalf("Runtime = %#v, want no process-env differences in new-session mode", resolved.Runtime)
	}
	if windowsEnv.snapshots != 1 {
		t.Fatalf("Windows Snapshot calls = %d, want 1", windowsEnv.snapshots)
	}
}

func TestResolveKeepsIDEAndProjectAsSideContexts(t *testing.T) {
	fileSystem := newTestFileSystem()
	fileSystem.addFile("/home/ada/.claude/settings.json", []byte(`{
		"env": {
			"ANTHROPIC_BASE_URL": "https://global.example.com",
			"ANTHROPIC_MODEL": "claude-global"
		}
	}`))
	fileSystem.addDir("/home/ada/.config/Code/User")
	fileSystem.addFile("/home/ada/.config/Code/User/settings.json", []byte(`{
		"claudeCode.selectedModel": "claude-ide-selected",
		"claudeCode.environmentVariables": [
			{ "name": "ANTHROPIC_BASE_URL", "value": "https://global.example.com" }
		]
	}`))
	fileSystem.addFile("/home/ada/project/.claude/settings.local.json", []byte(`{
		"env": {
			"ANTHROPIC_BASE_URL": "https://project.example.com",
			"ANTHROPIC_MODEL": "claude-global"
		}
	}`))

	read, err := Read(testSystem(fileSystem, "linux", "/home/ada", "/home/ada/project", map[string]string{
		"SHELL": "/bin/zsh",
	}), true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	resolved := Resolve(read)
	assertResolved(t, resolved.Current, core.VarAnthropicBaseURL, "https://global.example.com", core.SourceClaudeUserSettings)
	assertResolved(t, resolved.Persisted, core.VarAnthropicModel, "claude-global", core.SourceClaudeUserSettings)

	assertSideDifference(t, resolved.ProjectOverrides, DifferenceProjectOverride, core.VarAnthropicBaseURL, core.SourceProjectLocalSettings)
	if hasSideDifference(resolved.ProjectOverrides, core.VarAnthropicModel, core.SourceProjectLocalSettings) {
		t.Fatalf("project model equal to global should not be reported as an override: %#v", resolved.ProjectOverrides)
	}
	assertSideDifference(t, resolved.IDEDrift, DifferenceIDEDrift, core.VarAnthropicModel, core.SourceVSCodeSettings)
	if hasSideDifference(resolved.IDEDrift, core.VarAnthropicBaseURL, core.SourceVSCodeSettings) {
		t.Fatalf("IDE base URL equal to global should not be reported as drift: %#v", resolved.IDEDrift)
	}
}

func TestReadReportsMalformedSourceSeverity(t *testing.T) {
	fileSystem := newTestFileSystem()
	fileSystem.addFile("/home/ada/.claude/settings.json", []byte(`{"env":`))
	fileSystem.addDir("/home/ada/.config/Code/User")
	fileSystem.addFile("/home/ada/.config/Code/User/settings.json", []byte(`{"claudeCode.environmentVariables":`))
	fileSystem.addFile("/home/ada/project/.claude/settings.json", []byte(`{"env":`))

	read, err := Read(testSystem(fileSystem, "linux", "/home/ada", "/home/ada/project", map[string]string{
		"SHELL": "/bin/zsh",
	}), true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	got := issueStatuses(read.SourceIssues)
	want := map[core.SourceKind]core.DiagnosticStatus{
		core.SourceClaudeUserSettings: core.StatusFAIL,
		core.SourceVSCodeSettings:     core.StatusWARN,
		core.SourceProjectSettings:    core.StatusWARN,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("issue statuses = %v, want %v; issues: %#v", got, want, read.SourceIssues)
	}
}

func TestReadReportsPathInspectionErrorsAsSourceIssues(t *testing.T) {
	fileSystem := newTestFileSystem()
	fileSystem.addDir("/home/ada/.config/Code/User")
	fileSystem.statErrs = map[string]error{
		"/home/ada/.claude/settings.json":               fs.ErrPermission,
		"/home/ada/.zshrc":                              fs.ErrPermission,
		"/home/ada/.config/Code/User/settings.json":     fs.ErrPermission,
		"/home/ada/project/.claude/settings.local.json": fs.ErrPermission,
	}

	read, err := Read(testSystem(fileSystem, "linux", "/home/ada", "/home/ada/project", map[string]string{
		"SHELL": "/bin/zsh",
	}), true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	got := issueStatuses(read.SourceIssues)
	want := map[core.SourceKind]core.DiagnosticStatus{
		core.SourceClaudeUserSettings:   core.StatusFAIL,
		core.SourceShellProfile:         core.StatusFAIL,
		core.SourceVSCodeSettings:       core.StatusWARN,
		core.SourceProjectLocalSettings: core.StatusWARN,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("issue statuses = %v, want %v; issues: %#v", got, want, read.SourceIssues)
	}
}

func testSystem(fileSystem *testFileSystem, targetOS, home, work string, env map[string]string) system.System {
	return system.System{
		FS:       fileSystem,
		Env:      &testEnvironment{values: env},
		Platform: testPlatform{targetOS: targetOS, home: home, work: work},
	}
}

func assertResolved(t *testing.T, resolved core.ResolvedConfig, name, value string, kind core.SourceKind) core.ResolvedValue {
	t.Helper()
	got, ok := resolved.Get(name)
	if !ok {
		t.Fatalf("%s missing from %s", name, resolved.Name)
	}
	if got.Value != value || got.Source.Kind != kind {
		t.Fatalf("%s = {%q from %s}, want {%q from %s}", name, got.Value, got.Source.Kind, value, kind)
	}
	return got
}

func assertConflict(t *testing.T, conflicts []ConflictIssue, kind ConflictKind, name string) {
	t.Helper()
	for _, conflict := range conflicts {
		if conflict.Kind == kind && conflict.Name == name {
			return
		}
	}
	t.Fatalf("conflict %s for %s not found in %#v", kind, name, conflicts)
}

func assertRuntimeDifference(t *testing.T, differences []RuntimeDifference, kind DifferenceKind, name string) {
	t.Helper()
	for _, difference := range differences {
		if difference.Kind == kind && difference.Name == name {
			return
		}
	}
	t.Fatalf("runtime difference %s for %s not found in %#v", kind, name, differences)
}

func assertSideDifference(t *testing.T, differences []SideContextDifference, kind DifferenceKind, name string, source core.SourceKind) SideContextDifference {
	t.Helper()
	for _, difference := range differences {
		if difference.Kind == kind && difference.Name == name && difference.Context.Kind == source {
			return difference
		}
	}
	t.Fatalf("side difference %s for %s from %s not found in %#v", kind, name, source, differences)
	return SideContextDifference{}
}

func hasSideDifference(differences []SideContextDifference, name string, source core.SourceKind) bool {
	for _, difference := range differences {
		if difference.Name == name && difference.Context.Kind == source {
			return true
		}
	}
	return false
}

func issueStatuses(issues []SourceIssue) map[core.SourceKind]core.DiagnosticStatus {
	statuses := make(map[core.SourceKind]core.DiagnosticStatus, len(issues))
	for _, issue := range issues {
		statuses[issue.Source.Kind] = issue.Status
	}
	return statuses
}

type testFileSystem struct {
	files    map[string][]byte
	dirs     map[string]bool
	statErrs map[string]error
	reads    []string
	stats    []string
	writes   int
}

func newTestFileSystem() *testFileSystem {
	return &testFileSystem{
		files: make(map[string][]byte),
		dirs:  make(map[string]bool),
	}
}

func (f *testFileSystem) addFile(name string, data []byte) {
	f.files[name] = append([]byte(nil), data...)
	f.addDir(filepath.Dir(name))
}

func (f *testFileSystem) addDir(name string) {
	if name == "." || name == "" {
		return
	}
	f.dirs[name] = true
}

func (f *testFileSystem) ReadFile(name string) ([]byte, error) {
	f.reads = append(f.reads, name)
	data, ok := f.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}

func (f *testFileSystem) WriteFile(string, []byte, fs.FileMode) error {
	f.writes++
	return nil
}

func (f *testFileSystem) WriteFileExclusive(string, []byte, fs.FileMode) error {
	f.writes++
	return nil
}

func (f *testFileSystem) MkdirAll(string, fs.FileMode) error {
	f.writes++
	return nil
}

func (f *testFileSystem) Stat(name string) (fs.FileInfo, error) {
	f.stats = append(f.stats, name)
	if err := f.statErrs[name]; err != nil {
		return nil, err
	}
	if f.dirs[name] {
		return testFileInfo{name: name, dir: true}, nil
	}
	if data, ok := f.files[name]; ok {
		return testFileInfo{name: name, size: int64(len(data))}, nil
	}
	return nil, fs.ErrNotExist
}

func (f *testFileSystem) Rename(string, string) error {
	f.writes++
	return nil
}

func (f *testFileSystem) Remove(string) error {
	f.writes++
	return nil
}

func (f *testFileSystem) Chmod(string, fs.FileMode) error {
	f.writes++
	return nil
}

type testFileInfo struct {
	name string
	size int64
	dir  bool
}

func (i testFileInfo) Name() string {
	return i.name
}

func (i testFileInfo) Size() int64 {
	return i.size
}

func (i testFileInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o700
	}
	return 0o600
}

func (i testFileInfo) ModTime() time.Time {
	return time.Time{}
}

func (i testFileInfo) IsDir() bool {
	return i.dir
}

func (i testFileInfo) Sys() any {
	return nil
}

type testEnvironment struct {
	values map[string]string
}

func (e *testEnvironment) Environ() []string {
	values := make([]string, 0, len(e.values))
	for name, value := range e.values {
		values = append(values, name+"="+value)
	}
	sort.Strings(values)
	return values
}

func (e *testEnvironment) LookupEnv(name string) (string, bool) {
	value, ok := e.values[name]
	return value, ok
}

func (e *testEnvironment) Getenv(name string) string {
	return e.values[name]
}

func (e *testEnvironment) Setenv(name, value string) error {
	e.values[name] = value
	return nil
}

func (e *testEnvironment) Unsetenv(name string) error {
	delete(e.values, name)
	return nil
}

type testPlatform struct {
	targetOS string
	home     string
	work     string
}

func (p testPlatform) GOOS() string {
	return p.targetOS
}

func (p testPlatform) HomeDir() (string, error) {
	return p.home, nil
}

func (p testPlatform) WorkingDir() (string, error) {
	return p.work, nil
}

type testWindowsEnvironment struct {
	values    map[string]string
	snapshots int
	err       error
}

func (e *testWindowsEnvironment) Lookup(name string) (string, bool, error) {
	if e.err != nil {
		return "", false, e.err
	}
	value, ok := e.values[name]
	return value, ok, nil
}

func (e *testWindowsEnvironment) Snapshot(names []string) (map[string]string, error) {
	e.snapshots++
	if e.err != nil {
		return nil, e.err
	}
	values := make(map[string]string)
	for _, name := range names {
		if value, ok := e.values[name]; ok {
			values[name] = value
		}
	}
	return values, nil
}

func (e *testWindowsEnvironment) Set(name, value string) error {
	e.values[name] = value
	return nil
}

func (e *testWindowsEnvironment) Delete(name string) error {
	delete(e.values, name)
	return nil
}

type panicFileSystem struct{}

func (panicFileSystem) ReadFile(string) ([]byte, error) {
	panic("unexpected read")
}

func (panicFileSystem) WriteFile(string, []byte, fs.FileMode) error {
	panic("unexpected write")
}

func (panicFileSystem) WriteFileExclusive(string, []byte, fs.FileMode) error {
	panic("unexpected exclusive write")
}

func (panicFileSystem) MkdirAll(string, fs.FileMode) error {
	panic("unexpected mkdir")
}

func (panicFileSystem) Stat(string) (fs.FileInfo, error) {
	panic("unexpected stat")
}

func (panicFileSystem) Rename(string, string) error {
	panic("unexpected rename")
}

func (panicFileSystem) Remove(string) error {
	panic("unexpected remove")
}

func (panicFileSystem) Chmod(string, fs.FileMode) error {
	panic("unexpected chmod")
}

type panicEnvironment struct{}

func (panicEnvironment) Environ() []string {
	panic("unexpected environ")
}

func (panicEnvironment) LookupEnv(string) (string, bool) {
	panic("unexpected lookup env")
}

func (panicEnvironment) Getenv(string) string {
	panic("unexpected getenv")
}

func (panicEnvironment) Setenv(string, string) error {
	panic("unexpected setenv")
}

func (panicEnvironment) Unsetenv(string) error {
	panic("unexpected unsetenv")
}

type panicPlatform struct{}

func (panicPlatform) GOOS() string {
	panic("unexpected goos")
}

func (panicPlatform) HomeDir() (string, error) {
	panic("unexpected home")
}

func (panicPlatform) WorkingDir() (string, error) {
	panic("unexpected workdir")
}

type panicWindowsEnvironment struct{}

func (panicWindowsEnvironment) Lookup(string) (string, bool, error) {
	panic("unexpected windows env lookup")
}

func (panicWindowsEnvironment) Snapshot([]string) (map[string]string, error) {
	panic("unexpected windows env snapshot")
}

func (panicWindowsEnvironment) Set(string, string) error {
	panic("unexpected windows env set")
}

func (panicWindowsEnvironment) Delete(string) error {
	panic("unexpected windows env delete")
}
