package apply

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/diagnose"
	"github.com/r13v/llmgate/internal/settings"
	"github.com/r13v/llmgate/internal/shell"
	"github.com/r13v/llmgate/internal/system"
)

type Operation string

const (
	OperationCreateFile          Operation = "create file"
	OperationUpdateFile          Operation = "update file"
	OperationSetWindowsUserEnv   Operation = "set Windows user environment"
	OperationNoChanges           Operation = "no changes needed"
	OperationManualSetupRequired Operation = "manual setup required"
)

type Purpose string

const (
	PurposeSetup  Purpose = "setup"
	PurposeRepair Purpose = "repair"
)

type ValueState struct {
	Set   bool
	Value string
}

type Change struct {
	Name   string
	Old    ValueState
	New    ValueState
	Secret bool
}

type Plan struct {
	Purpose Purpose
	Targets []TargetPlan
	HomeDir string
	GOOS    string
}

type TargetPlan struct {
	Target         core.WriteTarget
	Operation      Operation
	Changes        []Change
	Warnings       []string
	Sensitive      bool
	OriginalExists bool
	Original       []byte
	Content        []byte
	ManualLines    []string
}

func (p Plan) HasChanges() bool {
	for _, target := range p.Targets {
		if target.Writes() {
			return true
		}
	}
	return false
}

func (t TargetPlan) Writes() bool {
	return t.Operation == OperationCreateFile ||
		t.Operation == OperationUpdateFile ||
		t.Operation == OperationSetWindowsUserEnv
}

func BuildSetupPlan(sys system.System, paths system.DiscoveredPaths, targets []core.WriteTarget, values core.SetupValues) (Plan, error) {
	fileSystem := fsOrDefault(sys.FS)
	setupValues := values.Map()
	plan := Plan{
		Purpose: PurposeSetup,
		HomeDir: paths.HomeDir,
		GOOS:    paths.GOOS,
	}

	for _, target := range targets {
		targetPlan, err := buildSetupTargetPlan(fileSystem, sys.WindowsEnv, paths, target, setupValues, values.Model)
		if err != nil {
			return Plan{}, err
		}
		plan.Targets = append(plan.Targets, targetPlan)
	}
	return plan, nil
}

func BuildRepairPlan(sys system.System, paths system.DiscoveredPaths, warnings []diagnose.RepairableStaleShellModelWarning) (Plan, error) {
	plan := Plan{
		Purpose: PurposeRepair,
		HomeDir: paths.HomeDir,
		GOOS:    paths.GOOS,
	}
	if len(warnings) == 0 {
		return plan, nil
	}

	byPath := make(map[string][]diagnose.RepairableStaleShellModelWarning)
	for _, warning := range warnings {
		if warning.Source.Kind != core.SourceShellProfile || warning.Source.Path == "" {
			continue
		}
		byPath[warning.Source.Path] = append(byPath[warning.Source.Path], warning)
	}
	for _, path := range orderedKeys(byPath) {
		target := core.WriteTarget{
			Kind:      core.WriteTargetShellProfile,
			Title:     core.WriteTargetTitle(core.WriteTargetShellProfile),
			Path:      path,
			Sensitive: true,
			Writable:  true,
			Exists:    true,
		}
		targetPlan, err := buildRepairShellPlan(fsOrDefault(sys.FS), paths, target, byPath[path])
		if err != nil {
			return Plan{}, err
		}
		plan.Targets = append(plan.Targets, targetPlan)
	}
	return plan, nil
}

func buildSetupTargetPlan(fileSystem system.FileSystem, windowsEnv system.WindowsUserEnvironment, paths system.DiscoveredPaths, target core.WriteTarget, values map[string]string, selectedModel string) (TargetPlan, error) {
	switch target.Kind {
	case core.WriteTargetClaudeUserSettings:
		return buildClaudePlan(fileSystem, target, values)
	case core.WriteTargetShellProfile:
		return buildShellPlan(fileSystem, paths, target, values, shell.ModeSetup)
	case core.WriteTargetManualShell:
		return buildManualShellPlan(target, values)
	case core.WriteTargetWindowsUserEnv:
		return buildWindowsPlan(windowsEnv, target, values)
	case core.WriteTargetVSCode, core.WriteTargetCursor:
		return buildIDEPlan(fileSystem, target, selectedModel, values)
	default:
		return TargetPlan{}, fmt.Errorf("unsupported write target kind %q", target.Kind)
	}
}

func buildClaudePlan(fileSystem system.FileSystem, target core.WriteTarget, values map[string]string) (TargetPlan, error) {
	original, exists, err := readTargetFile(fileSystem, target)
	if err != nil {
		return TargetPlan{}, err
	}
	originalForPlan := cloneBytes(original)
	parsed, err := parseClaudeForPlan(cloneBytes(original), exists)
	if err != nil {
		return TargetPlan{}, err
	}
	content, err := settings.UpsertClaude(dataForUpsert(cloneBytes(original), exists), values)
	if err != nil {
		return TargetPlan{}, err
	}
	changes := changesFromValues(parsed.Env, values)
	return fileTargetPlan(target, exists, originalForPlan, content, changes, setupFileWarnings(target))
}

func buildIDEPlan(fileSystem system.FileSystem, target core.WriteTarget, selectedModel string, values map[string]string) (TargetPlan, error) {
	original, exists, err := readTargetFile(fileSystem, target)
	if err != nil {
		return TargetPlan{}, err
	}
	originalForPlan := cloneBytes(original)
	parsed, err := parseIDEForPlan(cloneBytes(original), exists)
	if err != nil {
		return TargetPlan{}, err
	}
	content, err := settings.UpsertIDE(dataForUpsert(cloneBytes(original), exists), selectedModel, values)
	if err != nil {
		return TargetPlan{}, err
	}
	changes := changesFromValues(parsed.Environment, values)
	if !parsed.HasSelectedModel || parsed.SelectedModel != selectedModel {
		oldSelected := ValueState{Set: parsed.HasSelectedModel, Value: parsed.SelectedModel}
		changes = append(changes, change("claudeCode.selectedModel", oldSelected, ValueState{Set: true, Value: selectedModel}, false))
	}
	sortChanges(changes)
	return fileTargetPlan(target, exists, originalForPlan, content, changes, setupFileWarnings(target))
}

func buildShellPlan(fileSystem system.FileSystem, paths system.DiscoveredPaths, target core.WriteTarget, values map[string]string, mode shell.WriteMode) (TargetPlan, error) {
	syntax, err := syntaxForShell(paths.ShellProfile.Kind)
	if err != nil {
		return TargetPlan{}, err
	}
	original, exists, err := readTargetFile(fileSystem, target)
	if err != nil {
		return TargetPlan{}, err
	}
	originalForPlan := cloneBytes(original)
	profile, err := shell.ParseProfile(cloneBytes(original), syntax)
	if err != nil {
		return TargetPlan{}, err
	}
	content, result, err := shell.UpsertProfile(cloneBytes(original), syntax, values, mode)
	if err != nil {
		return TargetPlan{}, err
	}
	warnings := setupFileWarnings(target)
	for _, issue := range result.Skipped {
		warnings = append(warnings, issue.Summary)
	}
	changes := changesFromShellProfile(profile, values)
	return fileTargetPlan(target, exists, originalForPlan, content, changes, warnings)
}

func buildRepairShellPlan(fileSystem system.FileSystem, paths system.DiscoveredPaths, target core.WriteTarget, warnings []diagnose.RepairableStaleShellModelWarning) (TargetPlan, error) {
	values := make(map[string]string, len(warnings))
	oldValues := make(map[string]string, len(warnings))
	for _, warning := range warnings {
		values[warning.Name] = warning.CurrentValue.Value
		oldValues[warning.Name] = warning.StaleValue.Value
	}

	targetPlan, err := buildShellPlan(fileSystem, paths, target, values, shell.ModeRepair)
	if err != nil {
		return TargetPlan{}, err
	}
	if len(targetPlan.Warnings) > 0 {
		targetPlan.Warnings[0] = "Repair updates stale shell model assignments only; missing values are not appended."
	} else {
		targetPlan.Warnings = []string{"Repair updates stale shell model assignments only; missing values are not appended."}
	}
	syntax, err := syntaxForShell(paths.ShellProfile.Kind)
	if err != nil {
		return TargetPlan{}, err
	}
	profile, err := shell.ParseProfile(targetPlan.Original, syntax)
	if err != nil {
		return TargetPlan{}, err
	}
	targetPlan.Changes = repairChanges(profile, oldValues, values, targetPlan.Original, targetPlan.Content)
	if targetPlan.Operation == OperationNoChanges {
		targetPlan.Changes = nil
	}
	return targetPlan, nil
}

func buildManualShellPlan(target core.WriteTarget, values map[string]string) (TargetPlan, error) {
	lines, err := shell.ManualSetupLines(shell.SyntaxPOSIX, values)
	if err != nil {
		return TargetPlan{}, err
	}
	return TargetPlan{
		Target:      target,
		Operation:   OperationManualSetupRequired,
		Changes:     changesFromValues(nil, values),
		Warnings:    []string{"No supported shell profile was detected. Add equivalent exports manually without sharing the token."},
		Sensitive:   target.Sensitive,
		ManualLines: lines,
	}, nil
}

func fileTargetPlan(target core.WriteTarget, exists bool, original, content []byte, changes []Change, warnings []string) (TargetPlan, error) {
	operation := OperationUpdateFile
	if !exists {
		operation = OperationCreateFile
	}
	if bytes.Equal(original, content) {
		operation = OperationNoChanges
		changes = nil
	}
	return TargetPlan{
		Target:         target,
		Operation:      operation,
		Changes:        changes,
		Warnings:       warnings,
		Sensitive:      target.Sensitive,
		OriginalExists: exists,
		Original:       append([]byte(nil), original...),
		Content:        append([]byte(nil), content...),
	}, nil
}

func readTargetFile(fileSystem system.FileSystem, target core.WriteTarget) ([]byte, bool, error) {
	if !target.Exists {
		return nil, false, nil
	}
	data, err := fileSystem.ReadFile(target.Path)
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", target.Title, err)
	}
	return data, true, nil
}

func parseClaudeForPlan(data []byte, exists bool) (settings.Claude, error) {
	if !exists {
		return settings.Claude{Env: map[string]string{}}, nil
	}
	return settings.ParseClaude(data)
}

func parseIDEForPlan(data []byte, exists bool) (settings.IDE, error) {
	if !exists {
		return settings.IDE{Environment: map[string]string{}}, nil
	}
	return settings.ParseIDE(data)
}

func dataForUpsert(data []byte, exists bool) []byte {
	if !exists {
		return nil
	}
	return data
}

func cloneBytes(data []byte) []byte {
	return append([]byte(nil), data...)
}

func setupFileWarnings(target core.WriteTarget) []string {
	warnings := []string{"Writes gateway credentials, model mapping, and privacy/traffic defaults."}
	if target.Sensitive {
		warnings = append(warnings, "Sensitive file is written with user-only permissions when possible.")
	}
	if target.Exists {
		warnings = append(warnings, "A .llmgate.bak backup path will be reported after writing.")
	}
	return warnings
}

func changesFromValues(old map[string]string, desired map[string]string) []Change {
	changes := make([]Change, 0, len(desired))
	for _, name := range orderedNames(desired) {
		oldValue, oldOK := old[name]
		newValue := desired[name]
		if oldOK && oldValue == newValue {
			continue
		}
		changes = append(changes, change(name, ValueState{Set: oldOK, Value: oldValue}, ValueState{Set: true, Value: newValue}, core.IsSecret(name)))
	}
	return changes
}

func changesFromShellProfile(profile shell.Profile, desired map[string]string) []Change {
	old := make(map[string]string, len(profile.Values))
	for name, value := range profile.Values {
		old[name] = value.Value
	}
	return changesFromValues(old, desired)
}

func repairChanges(profile shell.Profile, oldValues, desired map[string]string, original, content []byte) []Change {
	if bytes.Equal(original, content) {
		return nil
	}
	changes := make([]Change, 0, len(desired))
	for _, name := range orderedNames(desired) {
		profileValue, profileOK := profile.Values[name]
		if !profileOK {
			continue
		}
		oldValue := profileValue.Value
		if warningValue, ok := oldValues[name]; ok {
			oldValue = warningValue
		}
		newValue := desired[name]
		if oldValue == newValue {
			continue
		}
		changes = append(changes, change(name, ValueState{Set: true, Value: oldValue}, ValueState{Set: true, Value: newValue}, core.IsSecret(name)))
	}
	return changes
}

func change(name string, oldValue, newValue ValueState, secret bool) Change {
	return Change{
		Name:   name,
		Old:    oldValue,
		New:    newValue,
		Secret: secret,
	}
}

func sortChanges(changes []Change) {
	order := make(map[string]int)
	for i, name := range core.AllManagedNames() {
		order[name] = i
	}
	sort.SliceStable(changes, func(i, j int) bool {
		left, leftOK := order[changes[i].Name]
		right, rightOK := order[changes[j].Name]
		switch {
		case leftOK && rightOK:
			return left < right
		case leftOK:
			return true
		case rightOK:
			return false
		default:
			return changes[i].Name < changes[j].Name
		}
	})
}

func orderedNames(values map[string]string) []string {
	names := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, name := range core.AllManagedNames() {
		if _, ok := values[name]; ok {
			names = append(names, name)
			seen[name] = true
		}
	}

	extras := make([]string, 0)
	for name := range values {
		if seen[name] {
			continue
		}
		extras = append(extras, name)
	}
	sort.Strings(extras)
	return append(names, extras...)
}

func orderedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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

func fsOrDefault(fileSystem system.FileSystem) system.FileSystem {
	if fileSystem == nil {
		return system.RealFileSystem{}
	}
	return fileSystem
}

func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

func isExist(err error) bool {
	return errors.Is(err, fs.ErrExist)
}
