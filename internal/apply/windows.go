package apply

import (
	"fmt"

	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/system"
)

func buildWindowsPlan(windowsEnv system.WindowsUserEnvironment, target core.WriteTarget, values map[string]string) (TargetPlan, error) {
	if windowsEnv == nil {
		windowsEnv = system.NewWindowsUserEnvironment()
	}
	existing, err := windowsEnv.Snapshot(core.AllManagedNames())
	if err != nil {
		return TargetPlan{}, fmt.Errorf("read Windows user environment: %w", err)
	}
	changes := changesFromValues(existing, values)
	operation := OperationSetWindowsUserEnv
	if len(changes) == 0 {
		operation = OperationNoChanges
	}
	return TargetPlan{
		Target:    target,
		Operation: operation,
		Changes:   changes,
		Warnings:  []string{"No file backup is created for Windows user environment updates; old and new values are shown in this plan."},
		Sensitive: target.Sensitive,
	}, nil
}

func applyWindowsTarget(windowsEnv system.WindowsUserEnvironment, target TargetPlan, result TargetResult) (TargetResult, error) {
	if windowsEnv == nil {
		windowsEnv = system.NewWindowsUserEnvironment()
	}
	if err := verifyWindowsTargetUnchanged(windowsEnv, target); err != nil {
		result.Status = ResultFailed
		result.Error = err.Error()
		return result, err
	}
	for _, change := range target.Changes {
		if !change.New.Set {
			continue
		}
		if err := windowsEnv.Set(change.Name, change.New.Value); err != nil {
			result.Status = ResultFailed
			result.Error = err.Error()
			return result, fmt.Errorf("set Windows user environment %s: %w", change.Name, err)
		}
	}
	result.Status = ResultWritten
	result.Changed = len(target.Changes) > 0
	return result, nil
}

func verifyWindowsTargetUnchanged(windowsEnv system.WindowsUserEnvironment, target TargetPlan) error {
	names := make([]string, 0, len(target.Changes))
	for _, change := range target.Changes {
		names = append(names, change.Name)
	}
	current, err := windowsEnv.Snapshot(names)
	if err != nil {
		return fmt.Errorf("verify Windows user environment before writing: %w", err)
	}
	for _, change := range target.Changes {
		value, ok := current[change.Name]
		if ok != change.Old.Set {
			return fmt.Errorf("%s changed after the apply plan was built: %s", target.Target.Title, change.Name)
		}
		if ok && value != change.Old.Value {
			return fmt.Errorf("%s changed after the apply plan was built: %s", target.Target.Title, change.Name)
		}
	}
	return nil
}
