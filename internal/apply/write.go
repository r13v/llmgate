package apply

import (
	"fmt"
	"io/fs"
	"time"

	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/system"
)

type ResultStatus string

const (
	ResultWritten ResultStatus = "written"
	ResultSkipped ResultStatus = "skipped"
	ResultManual  ResultStatus = "manual"
	ResultFailed  ResultStatus = "failed"
)

type ApplyOptions struct {
	Now func() time.Time
}

type Result struct {
	Targets []TargetResult
}

type TargetResult struct {
	Target     core.WriteTarget
	Operation  Operation
	Status     ResultStatus
	Changed    bool
	BackupPath string
	Changes    []Change
	Warnings   []string
	Sensitive  bool
	Error      string
}

func Apply(sys system.System, plan Plan, opts ApplyOptions) (Result, error) {
	fileSystem := fsOrDefault(sys.FS)
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}

	var result Result
	for _, target := range plan.Targets {
		targetResult, err := applyTarget(fileSystem, sys.WindowsEnv, target, now)
		result.Targets = append(result.Targets, targetResult)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func applyTarget(fileSystem system.FileSystem, windowsEnv system.WindowsUserEnvironment, target TargetPlan, now func() time.Time) (TargetResult, error) {
	result := TargetResult{
		Target:    target.Target,
		Operation: target.Operation,
		Changes:   append([]Change(nil), target.Changes...),
		Warnings:  append([]string(nil), target.Warnings...),
		Sensitive: target.Sensitive,
	}

	switch target.Operation {
	case OperationNoChanges:
		result.Status = ResultSkipped
		return result, nil
	case OperationManualSetupRequired:
		result.Status = ResultManual
		return result, nil
	case OperationSetWindowsUserEnv:
		return applyWindowsTarget(windowsEnv, target, result)
	case OperationCreateFile, OperationUpdateFile:
		return applyFileTarget(fileSystem, target, result, now)
	default:
		result.Status = ResultFailed
		result.Error = fmt.Sprintf("unsupported operation %q", target.Operation)
		return result, fmt.Errorf("%s: %s", target.Target.Title, result.Error)
	}
}

func applyFileTarget(fileSystem system.FileSystem, target TargetPlan, result TargetResult, now func() time.Time) (TargetResult, error) {
	if shouldCreateParent(target.Target.Kind) {
		if err := fileSystem.MkdirAll(parentDir(target.Target.Path), sensitiveDirMode); err != nil {
			result.Status = ResultFailed
			result.Error = err.Error()
			return result, fmt.Errorf("create parent directory for %s: %w", target.Target.Title, err)
		}
	}

	backupPath, err := createBackup(fileSystem, target, now())
	if err != nil {
		result.Status = ResultFailed
		result.Error = err.Error()
		return result, err
	}
	result.BackupPath = backupPath

	if err := writeAtomic(fileSystem, target.Target.Path, target.Content, fileMode(target.Sensitive)); err != nil {
		result.Status = ResultFailed
		result.Error = err.Error()
		return result, err
	}
	bestEffortChmod(fileSystem, target.Target.Path, fileMode(target.Sensitive))
	result.Status = ResultWritten
	result.Changed = true
	return result, nil
}

func writeAtomic(fileSystem system.FileSystem, path string, content []byte, mode fs.FileMode) error {
	tempPath := path + ".llmgate.tmp"
	if err := fileSystem.WriteFile(tempPath, content, mode); err != nil {
		return fmt.Errorf("write temporary file %s: %w", tempPath, err)
	}
	bestEffortChmod(fileSystem, tempPath, mode)
	if err := fileSystem.Rename(tempPath, path); err != nil {
		_ = fileSystem.Remove(tempPath)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func shouldCreateParent(kind core.WriteTargetKind) bool {
	return kind == core.WriteTargetClaudeUserSettings || kind == core.WriteTargetShellProfile
}

func parentDir(path string) string {
	index := lastSeparator(path)
	if index <= 0 {
		return "."
	}
	return path[:index]
}

func lastSeparator(path string) int {
	last := -1
	for i, r := range path {
		if r == '/' || r == '\\' {
			last = i
		}
	}
	return last
}
