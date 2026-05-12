package apply

import (
	"errors"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/diagnose"
	"github.com/r13v/llmgate/internal/system"
)

func TestBuildSetupPlanFreshTargetsRenderAndApply(t *testing.T) {
	fileSystem := newTestFileSystem()
	values := testSetupValues()
	paths := testUnixPaths(false)
	targets := []core.WriteTarget{
		writeTarget(core.WriteTargetClaudeUserSettings, "/home/ada/.claude/settings.json", false),
		writeTarget(core.WriteTargetShellProfile, "/home/ada/.zshrc", false),
		writeTarget(core.WriteTargetVSCode, "/home/ada/.config/Code/User/settings.json", false),
		writeTarget(core.WriteTargetCursor, "/home/ada/.config/Cursor/User/settings.json", false),
		{
			Kind:      core.WriteTargetManualShell,
			Title:     core.WriteTargetTitle(core.WriteTargetManualShell),
			Sensitive: true,
			Writable:  false,
		},
	}
	fileSystem.addDir("/home/ada/.config/Code/User")
	fileSystem.addDir("/home/ada/.config/Cursor/User")

	plan, err := BuildSetupPlan(system.System{FS: fileSystem}, paths, targets, values)
	if err != nil {
		t.Fatalf("BuildSetupPlan() error = %v", err)
	}
	assertOperations(t, plan, []Operation{
		OperationCreateFile,
		OperationCreateFile,
		OperationCreateFile,
		OperationCreateFile,
		OperationManualSetupRequired,
	})

	rendered := RenderPlan(plan, RenderOptions{})
	assertNotContains(t, rendered, values.AuthToken)
	assertContains(t, rendered, "sk-...7890")
	assertContains(t, rendered, "location: ~/.claude/settings.json")
	assertContains(t, rendered, "changes:")
	assertContains(t, rendered, "<unset> ->")
	assertContains(t, rendered, "sensitive: true")

	result, err := Apply(system.System{FS: fileSystem}, plan, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Targets[4].Status != ResultManual {
		t.Fatalf("manual target status = %s, want %s", result.Targets[4].Status, ResultManual)
	}
	assertFileContains(t, fileSystem, "/home/ada/.claude/settings.json", values.AuthToken)
	assertFileContains(t, fileSystem, "/home/ada/.zshrc", "export ANTHROPIC_MODEL='claude-sonnet-4'")
	assertFileContains(t, fileSystem, "/home/ada/.config/Code/User/settings.json", "claudeCode.selectedModel")
	assertFileContains(t, fileSystem, "/home/ada/.config/Cursor/User/settings.json", "claudeCode.environmentVariables")
	if fileSystem.exists("/home/ada/.claude/settings.json.llmgate.bak") {
		t.Fatalf("unexpected backup for created file")
	}

	renderedResult := RenderResult(result, RenderOptions{HomeDir: paths.HomeDir, GOOS: paths.GOOS})
	assertNotContains(t, renderedResult, values.AuthToken)
}

func TestUpdateExistingFileCreatesTimestampedBackupAtomicallyAndIdempotentRerunSkips(t *testing.T) {
	fileSystem := newTestFileSystem()
	values := testSetupValues()
	path := "/home/ada/.claude/settings.json"
	original := []byte("{\n  // keep this comment\n  \"env\": {\n    \"ANTHROPIC_AUTH_TOKEN\": \"sk-oldsecret1234\",\n    \"ANTHROPIC_BASE_URL\": \"https://old.example.com\"\n  },\n  \"theme\": \"dark\"\n}\n")
	fileSystem.addFile(path, original, 0o644)
	fileSystem.addFile(path+".llmgate.bak", []byte("previous backup"), 0o600)

	plan, err := BuildSetupPlan(system.System{FS: fileSystem}, testUnixPaths(true), []core.WriteTarget{
		writeTarget(core.WriteTargetClaudeUserSettings, path, true),
	}, values)
	if err != nil {
		t.Fatalf("BuildSetupPlan() error = %v", err)
	}
	if got := plan.Targets[0].Operation; got != OperationUpdateFile {
		t.Fatalf("operation = %s, want %s", got, OperationUpdateFile)
	}
	rendered := RenderPlan(plan, RenderOptions{})
	assertNotContains(t, rendered, "sk-oldsecret1234")
	assertNotContains(t, rendered, values.AuthToken)
	assertContains(t, rendered, "backup: .llmgate.bak path will be reported after writing")

	now := time.Date(2026, 5, 13, 1, 2, 3, 0, time.UTC)
	result, err := Apply(system.System{FS: fileSystem}, plan, ApplyOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	backupPath := path + ".llmgate.20260513-010203.bak"
	if result.Targets[0].BackupPath != backupPath {
		t.Fatalf("backup path = %q, want %q", result.Targets[0].BackupPath, backupPath)
	}
	if got := string(fileSystem.files[backupPath]); got != string(original) {
		t.Fatalf("backup content changed:\n%s", got)
	}
	assertFileContains(t, fileSystem, path, "\"theme\": \"dark\"")
	assertFileContains(t, fileSystem, path, values.AuthToken)
	assertRename(t, fileSystem, path+".llmgate.tmp", path)
	if mode := fileSystem.modes[path]; mode != 0o600 {
		t.Fatalf("final mode = %v, want 0600", mode)
	}
	if mode := fileSystem.modes[backupPath]; mode != 0o600 {
		t.Fatalf("backup mode = %v, want 0600", mode)
	}

	writesAfterFirstApply := len(fileSystem.writes)
	renamesAfterFirstApply := len(fileSystem.renames)
	secondPlan, err := BuildSetupPlan(system.System{FS: fileSystem}, testUnixPaths(true), []core.WriteTarget{
		writeTarget(core.WriteTargetClaudeUserSettings, path, true),
	}, values)
	if err != nil {
		t.Fatalf("second BuildSetupPlan() error = %v", err)
	}
	if got := secondPlan.Targets[0].Operation; got != OperationNoChanges {
		t.Fatalf("second operation = %s, want %s", got, OperationNoChanges)
	}
	secondResult, err := Apply(system.System{FS: fileSystem}, secondPlan, ApplyOptions{})
	if err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if secondResult.Targets[0].Status != ResultSkipped {
		t.Fatalf("second result status = %s, want %s", secondResult.Targets[0].Status, ResultSkipped)
	}
	if len(fileSystem.writes) != writesAfterFirstApply || len(fileSystem.renames) != renamesAfterFirstApply {
		t.Fatalf("idempotent apply wrote files: writes %d->%d renames %d->%d", writesAfterFirstApply, len(fileSystem.writes), renamesAfterFirstApply, len(fileSystem.renames))
	}
}

func TestUpdateExistingFileAvoidsTimestampedBackupCollision(t *testing.T) {
	fileSystem := newTestFileSystem()
	values := testSetupValues()
	path := "/home/ada/.claude/settings.json"
	original := []byte(`{"env":{"ANTHROPIC_MODEL":"old-model"}}`)
	fileSystem.addFile(path, original, 0o600)
	fileSystem.addFile(path+".llmgate.bak", []byte("primary backup"), 0o600)
	fileSystem.addFile(path+".llmgate.20260513-010203.bak", []byte("timestamp backup"), 0o600)

	plan, err := BuildSetupPlan(system.System{FS: fileSystem}, testUnixPaths(true), []core.WriteTarget{
		writeTarget(core.WriteTargetClaudeUserSettings, path, true),
	}, values)
	if err != nil {
		t.Fatalf("BuildSetupPlan() error = %v", err)
	}
	result, err := Apply(system.System{FS: fileSystem}, plan, ApplyOptions{
		Now: func() time.Time {
			return time.Date(2026, 5, 13, 1, 2, 3, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	backupPath := path + ".llmgate.20260513-010203.1.bak"
	if result.Targets[0].BackupPath != backupPath {
		t.Fatalf("backup path = %q, want %q", result.Targets[0].BackupPath, backupPath)
	}
	if got := string(fileSystem.files[backupPath]); got != string(original) {
		t.Fatalf("backup content = %q, want original", got)
	}
	if got := string(fileSystem.files[path+".llmgate.20260513-010203.bak"]); got != "timestamp backup" {
		t.Fatalf("existing timestamp backup was overwritten: %q", got)
	}
}

func TestMalformedSettingsRejectedBeforeWrite(t *testing.T) {
	fileSystem := newTestFileSystem()
	path := "/home/ada/.claude/settings.json"
	fileSystem.addFile(path, []byte(`{"env":`), 0o600)

	_, err := BuildSetupPlan(system.System{FS: fileSystem}, testUnixPaths(true), []core.WriteTarget{
		writeTarget(core.WriteTargetClaudeUserSettings, path, true),
	}, testSetupValues())
	if err == nil {
		t.Fatalf("BuildSetupPlan() error = nil, want malformed settings error")
	}
	if len(fileSystem.writes) != 0 || len(fileSystem.renames) != 0 {
		t.Fatalf("malformed plan wrote files: writes=%v renames=%v", fileSystem.writes, fileSystem.renames)
	}
}

func TestRepairPlanUpdatesOnlyStaleSimpleShellAssignments(t *testing.T) {
	fileSystem := newTestFileSystem()
	path := "/home/ada/.zshrc"
	fileSystem.addFile(path, []byte(strings.Join([]string{
		"export ANTHROPIC_MODEL='old-model' # keep",
		"export ANTHROPIC_DEFAULT_HAIKU_MODEL=$(choose-haiku)",
		"export OTHER='untouched'",
		"",
	}, "\n")), 0o600)
	paths := testUnixPaths(true)
	warnings := []diagnose.RepairableStaleShellModelWarning{
		{
			Name:         core.VarAnthropicModel,
			StaleValue:   resolved(core.VarAnthropicModel, "old-model", path),
			CurrentValue: resolved(core.VarAnthropicModel, "new-model", ""),
			Source:       core.SourceLabel{Kind: core.SourceShellProfile, Path: path},
		},
		{
			Name:         core.VarAnthropicDefaultHaikuModel,
			StaleValue:   resolved(core.VarAnthropicDefaultHaikuModel, "old-haiku", path),
			CurrentValue: resolved(core.VarAnthropicDefaultHaikuModel, "new-haiku", ""),
			Source:       core.SourceLabel{Kind: core.SourceShellProfile, Path: path},
		},
	}

	plan, err := BuildRepairPlan(system.System{FS: fileSystem}, paths, warnings)
	if err != nil {
		t.Fatalf("BuildRepairPlan() error = %v", err)
	}
	if got := plan.Targets[0].Operation; got != OperationUpdateFile {
		t.Fatalf("operation = %s, want %s", got, OperationUpdateFile)
	}
	if names := changeNames(plan.Targets[0].Changes); !reflect.DeepEqual(names, []string{core.VarAnthropicModel}) {
		t.Fatalf("repair changes = %v, want only %s", names, core.VarAnthropicModel)
	}
	rendered := RenderPlan(plan, RenderOptions{})
	assertContains(t, rendered, "repair stale shell model assignments")
	assertContains(t, rendered, "requires manual review and was not modified")

	_, err = Apply(system.System{FS: fileSystem}, plan, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	final := string(fileSystem.files[path])
	assertContains(t, final, "export ANTHROPIC_MODEL='new-model' # keep")
	assertContains(t, final, "export ANTHROPIC_DEFAULT_HAIKU_MODEL=$(choose-haiku)")
	assertNotContains(t, final, "new-haiku")
	assertContains(t, final, "export OTHER='untouched'")
}

func TestWindowsUserEnvironmentPlanApplyAndIdempotency(t *testing.T) {
	values := testSetupValues()
	windowsEnv := &testWindowsEnvironment{values: map[string]string{
		core.VarAnthropicAuthToken: "sk-oldwindows1234",
		core.VarAnthropicBaseURL:   values.BaseURL,
	}}
	target := core.WriteTarget{
		Kind:      core.WriteTargetWindowsUserEnv,
		Title:     core.WriteTargetTitle(core.WriteTargetWindowsUserEnv),
		Sensitive: true,
		Writable:  true,
		Exists:    true,
	}
	paths := system.DiscoveredPaths{GOOS: "windows", HomeDir: `C:\Users\Ada`}

	plan, err := BuildSetupPlan(system.System{WindowsEnv: windowsEnv}, paths, []core.WriteTarget{target}, values)
	if err != nil {
		t.Fatalf("BuildSetupPlan() error = %v", err)
	}
	if got := plan.Targets[0].Operation; got != OperationSetWindowsUserEnv {
		t.Fatalf("operation = %s, want %s", got, OperationSetWindowsUserEnv)
	}
	if hasChange(plan.Targets[0].Changes, core.VarAnthropicBaseURL) {
		t.Fatalf("unchanged base URL should not be included in Windows changes: %#v", plan.Targets[0].Changes)
	}
	rendered := RenderPlan(plan, RenderOptions{})
	assertNotContains(t, rendered, values.AuthToken)
	assertContains(t, rendered, "No file backup")

	result, err := Apply(system.System{WindowsEnv: windowsEnv}, plan, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Targets[0].BackupPath != "" {
		t.Fatalf("Windows result backup path = %q, want empty", result.Targets[0].BackupPath)
	}
	if windowsEnv.values[core.VarAnthropicAuthToken] != values.AuthToken {
		t.Fatalf("Windows token was not updated")
	}
	if contains(windowsEnv.sets, core.VarAnthropicBaseURL) {
		t.Fatalf("unchanged base URL was written: %v", windowsEnv.sets)
	}

	secondPlan, err := BuildSetupPlan(system.System{WindowsEnv: windowsEnv}, paths, []core.WriteTarget{target}, values)
	if err != nil {
		t.Fatalf("second BuildSetupPlan() error = %v", err)
	}
	if got := secondPlan.Targets[0].Operation; got != OperationNoChanges {
		t.Fatalf("second operation = %s, want %s", got, OperationNoChanges)
	}
}

func TestApplyFailureStopsAndReportsFailedTarget(t *testing.T) {
	fileSystem := newTestFileSystem()
	fileSystem.writeErr = errors.New("disk full")
	path := "/home/ada/.claude/settings.json"
	plan, err := BuildSetupPlan(system.System{FS: fileSystem}, testUnixPaths(false), []core.WriteTarget{
		writeTarget(core.WriteTargetClaudeUserSettings, path, false),
	}, testSetupValues())
	if err != nil {
		t.Fatalf("BuildSetupPlan() error = %v", err)
	}

	result, err := Apply(system.System{FS: fileSystem}, plan, ApplyOptions{})
	if err == nil {
		t.Fatalf("Apply() error = nil, want failure")
	}
	if len(result.Targets) != 1 || result.Targets[0].Status != ResultFailed {
		t.Fatalf("failed result = %#v, want one failed target", result)
	}
	if fileSystem.exists(path) {
		t.Fatalf("final file exists after failed temp write")
	}
}

func testSetupValues() core.SetupValues {
	return core.SetupValues{
		AuthToken:   "sk-testsecret1234567890",
		BaseURL:     "https://gateway.example.com",
		Model:       "claude-sonnet-4",
		HaikuModel:  "claude-haiku-4",
		SonnetModel: "claude-sonnet-4",
		OpusModel:   "claude-opus-4",
	}
}

func testUnixPaths(shellExists bool) system.DiscoveredPaths {
	return system.DiscoveredPaths{
		GOOS:    "linux",
		HomeDir: "/home/ada",
		ShellProfile: system.ShellProfile{
			Kind:     system.ShellZsh,
			Path:     "/home/ada/.zshrc",
			Detected: true,
			Exists:   shellExists,
		},
	}
}

func writeTarget(kind core.WriteTargetKind, path string, exists bool) core.WriteTarget {
	return core.WriteTarget{
		Kind:      kind,
		Title:     core.WriteTargetTitle(kind),
		Path:      path,
		Sensitive: true,
		Writable:  true,
		Exists:    exists,
	}
}

func resolved(name, value, path string) core.ResolvedValue {
	return core.ResolvedValue{
		Name:   name,
		Value:  value,
		Source: core.SourceLabel{Kind: core.SourceShellProfile, Path: path},
		Secret: core.IsSecret(name),
	}
}

func assertOperations(t *testing.T, plan Plan, want []Operation) {
	t.Helper()
	got := make([]Operation, 0, len(plan.Targets))
	for _, target := range plan.Targets {
		got = append(got, target.Operation)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("operations = %v, want %v", got, want)
	}
}

func assertFileContains(t *testing.T, fileSystem *testFileSystem, path, want string) {
	t.Helper()
	data, ok := fileSystem.files[path]
	if !ok {
		t.Fatalf("file %s missing", path)
	}
	assertContains(t, string(data), want)
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output missing %q:\n%s", want, got)
	}
}

func assertNotContains(t *testing.T, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Fatalf("output contains %q:\n%s", want, got)
	}
}

func assertRename(t *testing.T, fileSystem *testFileSystem, oldPath, newPath string) {
	t.Helper()
	for _, rename := range fileSystem.renames {
		if rename.oldPath == oldPath && rename.newPath == newPath {
			return
		}
	}
	t.Fatalf("rename %s -> %s not found in %#v", oldPath, newPath, fileSystem.renames)
}

func changeNames(changes []Change) []string {
	names := make([]string, 0, len(changes))
	for _, change := range changes {
		names = append(names, change.Name)
	}
	sort.Strings(names)
	return names
}

func hasChange(changes []Change, name string) bool {
	for _, change := range changes {
		if change.Name == name {
			return true
		}
	}
	return false
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

type testFileSystem struct {
	files    map[string][]byte
	modes    map[string]fs.FileMode
	dirs     map[string]bool
	writes   []string
	renames  []testRename
	writeErr error
}

type testRename struct {
	oldPath string
	newPath string
}

func newTestFileSystem() *testFileSystem {
	return &testFileSystem{
		files: make(map[string][]byte),
		modes: make(map[string]fs.FileMode),
		dirs:  make(map[string]bool),
	}
}

func (f *testFileSystem) addFile(path string, data []byte, mode fs.FileMode) {
	f.files[path] = append([]byte(nil), data...)
	f.modes[path] = mode
	f.addDir(parentDir(path))
}

func (f *testFileSystem) addDir(path string) {
	if path == "" || path == "." {
		return
	}
	f.dirs[path] = true
}

func (f *testFileSystem) exists(path string) bool {
	_, ok := f.files[path]
	return ok
}

func (f *testFileSystem) ReadFile(path string) ([]byte, error) {
	data, ok := f.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}

func (f *testFileSystem) WriteFile(path string, data []byte, mode fs.FileMode) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.writes = append(f.writes, path)
	f.files[path] = append([]byte(nil), data...)
	f.modes[path] = mode
	f.addDir(parentDir(path))
	return nil
}

func (f *testFileSystem) MkdirAll(path string, _ fs.FileMode) error {
	f.addDir(path)
	return nil
}

func (f *testFileSystem) Stat(path string) (fs.FileInfo, error) {
	if f.dirs[path] {
		return testFileInfo{name: path, mode: fs.ModeDir | 0o700, dir: true}, nil
	}
	data, ok := f.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return testFileInfo{name: path, size: int64(len(data)), mode: f.modes[path]}, nil
}

func (f *testFileSystem) Rename(oldPath, newPath string) error {
	data, ok := f.files[oldPath]
	if !ok {
		return fs.ErrNotExist
	}
	f.renames = append(f.renames, testRename{oldPath: oldPath, newPath: newPath})
	f.files[newPath] = append([]byte(nil), data...)
	f.modes[newPath] = f.modes[oldPath]
	delete(f.files, oldPath)
	delete(f.modes, oldPath)
	return nil
}

func (f *testFileSystem) Remove(path string) error {
	delete(f.files, path)
	delete(f.modes, path)
	return nil
}

func (f *testFileSystem) Chmod(path string, mode fs.FileMode) error {
	if _, ok := f.files[path]; ok {
		f.modes[path] = mode
		return nil
	}
	if f.dirs[path] {
		return nil
	}
	return fs.ErrNotExist
}

type testFileInfo struct {
	name string
	size int64
	mode fs.FileMode
	dir  bool
}

func (i testFileInfo) Name() string {
	return i.name
}

func (i testFileInfo) Size() int64 {
	return i.size
}

func (i testFileInfo) Mode() fs.FileMode {
	return i.mode
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

type testWindowsEnvironment struct {
	values map[string]string
	sets   []string
	err    error
}

func (e *testWindowsEnvironment) Lookup(name string) (string, bool, error) {
	if e.err != nil {
		return "", false, e.err
	}
	value, ok := e.values[name]
	return value, ok, nil
}

func (e *testWindowsEnvironment) Snapshot(names []string) (map[string]string, error) {
	if e.err != nil {
		return nil, e.err
	}
	values := make(map[string]string, len(names))
	for _, name := range names {
		if value, ok := e.values[name]; ok {
			values[name] = value
		}
	}
	return values, nil
}

func (e *testWindowsEnvironment) Set(name, value string) error {
	if e.err != nil {
		return e.err
	}
	if e.values == nil {
		e.values = make(map[string]string)
	}
	e.sets = append(e.sets, name)
	e.values[name] = value
	return nil
}

func (e *testWindowsEnvironment) Delete(name string) error {
	if e.err != nil {
		return e.err
	}
	delete(e.values, name)
	return nil
}
