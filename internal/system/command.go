package system

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func (r CommandResult) CombinedOutput() string {
	if r.Stderr == "" {
		return r.Stdout
	}
	if r.Stdout == "" {
		return r.Stderr
	}
	return r.Stdout + r.Stderr
}

type RealCommandRunner struct{}

func (RealCommandRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	command := exec.CommandContext(ctx, name, args...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()
	result := CommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}
	if err == nil {
		return result, nil
	}

	result.ExitCode = -1
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ProcessState != nil {
		result.ExitCode = exitError.ExitCode()
	}
	return result, err
}

func ClaudeVersion(ctx context.Context, runner CommandRunner) (string, error) {
	if runner == nil {
		runner = RealCommandRunner{}
	}

	result, err := runner.Run(ctx, "claude", "--version")
	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		output = strings.TrimSpace(result.Stderr)
	}
	if err != nil {
		return output, fmt.Errorf("claude --version failed: %w", err)
	}
	return output, nil
}
