package e2e

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/charmbracelet/x/xpty"
	"github.com/r13v/llmgate/internal/wizard"
)

func TestXPTYPasswordTokenPromptSmoke(t *testing.T) {
	pty := newUnixPty(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan ptyPromptResult, 1)
	go func() {
		value, err := (wizard.HuhPrompter{
			In:     pty.Slave(),
			Output: pty.Slave(),
		}).Input(ctx, wizard.InputPrompt{
			Title:    "Gateway API token",
			Required: true,
			Secret:   true,
		})
		done <- ptyPromptResult{value: value, err: err}
	}()

	time.Sleep(150 * time.Millisecond)
	if _, err := pty.Write([]byte(testToken + "\r")); err != nil {
		t.Fatalf("write token to pty: %v", err)
	}

	got := waitPromptResult(t, pty, done)
	if got.err != nil {
		t.Fatalf("Input() error = %v", got.err)
	}
	if got.value != testToken {
		t.Fatalf("Input() value = %q, want test token", got.value)
	}
}

func TestXPTYPromptCancellationSmoke(t *testing.T) {
	pty := newUnixPty(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := (wizard.HuhPrompter{
			In:     pty.Slave(),
			Output: pty.Slave(),
		}).Confirm(ctx, wizard.ConfirmPrompt{
			Title:       "Allow llmgate to inspect local Claude Code configuration?",
			Affirmative: "Allow",
			Negative:    "Decline",
		})
		done <- err
	}()

	time.Sleep(150 * time.Millisecond)
	if _, err := pty.Write([]byte{3}); err != nil {
		t.Fatalf("write ctrl-c to pty: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, wizard.ErrCanceled) {
			t.Fatalf("Confirm() error = %v, want ErrCanceled", err)
		}
	case <-time.After(3 * time.Second):
		_ = pty.Close()
		t.Fatal("Confirm() did not return after cancellation")
	}
}

func TestXPTYWizardStartupCancellationSmoke(t *testing.T) {
	pty := newUnixPty(t)
	h := newHarness(t)

	done := make(chan error, 1)
	go func() {
		_, err := h.runWithPrompter(wizard.HuhPrompter{
			In:     pty.Slave(),
			Output: pty.Slave(),
		})
		done <- err
	}()

	time.Sleep(150 * time.Millisecond)
	if _, err := pty.Write([]byte{3}); err != nil {
		t.Fatalf("write ctrl-c to pty: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, wizard.ErrStartupDeclined) {
			t.Fatalf("Run() error = %v, want ErrStartupDeclined", err)
		}
	case <-time.After(3 * time.Second):
		_ = pty.Close()
		t.Fatal("wizard did not return after startup cancellation")
	}
	if h.fs.readOps != 0 || h.fs.statOps != 0 || h.commands.calls != 0 {
		t.Fatalf("startup cancellation touched local state: reads=%d stats=%d commands=%d", h.fs.readOps, h.fs.statOps, h.commands.calls)
	}
}

func newUnixPty(t *testing.T) *xpty.UnixPty {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY smoke tests are skipped on Windows")
	}
	pty, err := xpty.NewUnixPty(80, 24)
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	t.Cleanup(func() {
		_ = pty.Close()
	})
	return pty
}

type ptyPromptResult struct {
	value string
	err   error
}

func waitPromptResult(t *testing.T, pty *xpty.UnixPty, done <-chan ptyPromptResult) ptyPromptResult {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(3 * time.Second):
		_ = pty.Close()
		t.Fatal("prompt did not return")
	}
	return ptyPromptResult{}
}
