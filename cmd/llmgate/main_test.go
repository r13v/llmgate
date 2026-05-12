package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/r13v/llmgate/internal/wizard"
)

func TestRunHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("help output missing usage:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("help wrote stderr: %q", stderr.String())
	}
}

func TestRunVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run returned %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "llmgate ") {
		t.Fatalf("version output missing product name:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "platform: ") {
		t.Fatalf("version output missing platform:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("version wrote stderr: %q", stderr.String())
	}
}

func TestRunNoArgsRequiresInteractiveTerminal(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(nil, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("no-arg failure wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "requires an interactive terminal") {
		t.Fatalf("no-arg stderr missing interactive terminal message: %q", stderr.String())
	}
}

func TestRunNoArgsStartupDeclineIsAlreadyHandled(t *testing.T) {
	originalRunWizard := runWizardFn
	runWizardFn = func(_ io.Writer) error {
		return wizard.ErrStartupDeclined
	}
	defer func() {
		runWizardFn = originalRunWizard
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(nil, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("startup decline wrote stdout: %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("startup decline wrote stderr: %q", stderr.String())
	}
}

func TestRunNoArgsPropagatesOtherWizardErrors(t *testing.T) {
	originalRunWizard := runWizardFn
	runWizardFn = func(_ io.Writer) error {
		return errors.New("boom")
	}
	defer func() {
		runWizardFn = originalRunWizard
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(nil, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("wizard error wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("wizard error missing stderr: %q", stderr.String())
	}
}

func TestRunUnknownFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--unknown"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unknown flag wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("unknown flag stderr missing parse error: %q", stderr.String())
	}
}

func TestRunUnexpectedArgument(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"setup"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("unexpected argument wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unexpected argument "setup"`) {
		t.Fatalf("unexpected argument stderr missing error: %q", stderr.String())
	}
}

func TestRunArgumentErrorsRedactTokenLikeSecrets(t *testing.T) {
	tests := []struct {
		name string
		args []string
		leak string
		want string
	}{
		{
			name: "long unexpected argument",
			args: []string{"sk-test-token-1234567890"},
			leak: "sk-test-token-1234567890",
			want: "sk-...7890",
		},
		{
			name: "long flag value",
			args: []string{"--version=sk-test-token-1234567890"},
			leak: "sk-test-token-1234567890",
			want: "sk-...7890",
		},
		{
			name: "short unexpected argument",
			args: []string{"sk-abc"},
			leak: "sk-abc",
			want: "sk-[redacted]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			code := run(tt.args, &stdout, &stderr)

			if code != 2 {
				t.Fatalf("run returned %d, want 2", code)
			}
			if stdout.Len() != 0 {
				t.Fatalf("argument error wrote stdout: %q", stdout.String())
			}
			if strings.Contains(stderr.String(), tt.leak) {
				t.Fatalf("argument error leaked token: %q", stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("argument error missing masked token: %q", stderr.String())
			}
		})
	}
}
