package e2e

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/r13v/llmgate/internal/wizard"
)

func TestAccessibleWizardStartupDeclineIsPrivate(t *testing.T) {
	h := newHarness(t)

	output, err := h.runAccessible("n\n")
	if !errors.Is(err, wizard.ErrStartupDeclined) {
		t.Fatalf("Run() error = %v, want ErrStartupDeclined\n%s", err, output)
	}
	assertContains(t, output, "No files were read or changed.")
	if h.fs.readOps != 0 || h.fs.statOps != 0 {
		t.Fatalf("decline touched filesystem: reads=%d stats=%d", h.fs.readOps, h.fs.statOps)
	}
	if h.fs.mutationOps() != 0 {
		t.Fatalf("decline mutated filesystem %d time(s)", h.fs.mutationOps())
	}
	if h.env.readOps() != 0 || h.env.mutationOps != 0 {
		t.Fatalf("decline touched process environment: reads=%d mutations=%d", h.env.readOps(), h.env.mutationOps)
	}
	if h.winEnv.readOps() != 0 || h.winEnv.mutationOps() != 0 {
		t.Fatalf("decline touched Windows user environment: reads=%d mutations=%d", h.winEnv.readOps(), h.winEnv.mutationOps())
	}
	if h.commands.calls != 0 {
		t.Fatalf("decline ran %d command(s)", h.commands.calls)
	}
	if h.gateway.listCalls != 0 || h.gateway.probeCalls != 0 {
		t.Fatalf("decline made gateway calls: list=%d probe=%d", h.gateway.listCalls, h.gateway.probeCalls)
	}
}

func TestAccessibleWizardFreshSetupSmoke(t *testing.T) {
	h := newHarness(t)
	input := strings.Join([]string{
		"y",
		"1",
		testToken,
		h.gateway.url(),
		"y",
		"0",
		"y",
		"",
	}, "\n")

	output, err := h.runAccessible(input)
	if err != nil {
		t.Fatalf("Run() error = %v\n%s", err, output)
	}

	assertContains(t, output, "Configured")
	assertContains(t, output, "Restart your terminal and IDE")
	assertContains(t, output, "~/.claude/settings.json")
	assertNotContains(t, output, "/home/ada/.claude/settings.json")
	assertNoSecretLeak(t, output, testToken)
	assertFileContains(t, h.fs, "/home/ada/.claude/settings.json", testToken)
	assertFileContains(t, h.fs, "/home/ada/.zshrc", "export ANTHROPIC_MODEL='"+sonnetModel+"'")
	if h.gateway.listCalls == 0 {
		t.Fatalf("fresh setup did not list models")
	}
	if h.gateway.probePingBodies == 0 {
		t.Fatalf("fresh setup did not send ping model probes")
	}
}

func TestAccessibleWizardGatewayErrorRedactsSecret(t *testing.T) {
	h := newHarness(t, withGatewayOptions(t, fakeGatewayOptions{
		models: recommendedModels,
		listResponses: []gatewayResponse{{
			status: http.StatusBadGateway,
			body:   `{"detail":"temporary failure for ` + leakedToken + `"}`,
		}},
		includeTokenBody: true,
	}))
	input := strings.Join([]string{
		"y",
		"1",
		leakedToken,
		h.gateway.url(),
		"3",
		"",
	}, "\n")

	output, err := h.runAccessible(input)
	if err != nil {
		t.Fatalf("Run() error = %v\n%s", err, output)
	}
	assertContains(t, output, "Gateway validation failed")
	assertNoSecretLeak(t, output, leakedToken)
	if h.fs.mutationOps() != 0 {
		t.Fatalf("gateway failure wrote filesystem %d time(s)\n%s", h.fs.mutationOps(), output)
	}
}

func TestWizardNonInteractiveFailure(t *testing.T) {
	h := newHarness(t)
	output, err := h.runScripted(nil, nonInteractive())
	if !errors.Is(err, wizard.ErrNonInteractive) {
		t.Fatalf("Run() error = %v, want ErrNonInteractive\n%s", err, output)
	}
	if output != "" {
		t.Fatalf("non-interactive failure wrote output:\n%s", output)
	}
}
