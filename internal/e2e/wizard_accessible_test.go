package e2e

import (
	"errors"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/r13v/llmgate/internal/core"
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

func TestAccessibleWizardStructuredDiagnosticSummaryGroupsTokenFindings(t *testing.T) {
	h := newHarness(t, withGatewayOptions(t, fakeGatewayOptions{includeTokenBody: true}))
	h.env.values[core.VarAnthropicAuthToken] = leakedToken
	addAccessibleFile(h.fs, "/home/ada/.claude/settings.json", []byte(`{"env":{`+
		`"ANTHROPIC_AUTH_TOKEN":"`+testToken+`",`+
		`"ANTHROPIC_BASE_URL":"`+h.gateway.url()+`",`+
		`"ANTHROPIC_MODEL":"`+sonnetModel+`",`+
		`"ANTHROPIC_DEFAULT_HAIKU_MODEL":"`+haikuModel+`",`+
		`"ANTHROPIC_DEFAULT_SONNET_MODEL":"`+sonnetModel+`",`+
		`"ANTHROPIC_DEFAULT_OPUS_MODEL":"`+opusModel+`"`+
		`}}`+"\n"), 0o600)
	addAccessibleFile(h.fs, "/home/ada/.zshrc", []byte("export ANTHROPIC_AUTH_TOKEN='"+leakedToken+"'\n"), 0o600)
	h.fs.addDir("/home/ada/.config/Code/User")
	h.fs.addDir("/home/ada/.config/Cursor/User")
	ideTokenSettings := []byte(`{"claudeCode.environmentVariables":[{"name":"ANTHROPIC_AUTH_TOKEN","value":"` + testToken + `"}]}` + "\n")
	addAccessibleFile(h.fs, "/home/ada/.config/Code/User/settings.json", ideTokenSettings, 0o600)
	addAccessibleFile(h.fs, "/home/ada/.config/Cursor/User/settings.json", ideTokenSettings, 0o600)

	output, err := h.runAccessible("y\n3\n")
	if err != nil {
		t.Fatalf("Run() error = %v\n%s", err, output)
	}

	assertContains(t, output, "Initial diagnostics: FAIL")
	assertContains(t, output, "FAIL Gateway: token rejected")
	assertContains(t, output, "WARN Config: ANTHROPIC_AUTH_TOKEN differs across sources")
	assertContains(t, output, "WARN IDE: ANTHROPIC_AUTH_TOKEN differs from current environment")
	assertContains(t, output, "evidence: request URL:")
	assertContains(t, output, "evidence: HTTP status: 401")
	assertContains(t, output, "fix:")
	assertNotContains(t, output, "[Gateway / Gateway validation] gateway validation failed")
	assertNotContains(t, output, "[Config Source Conflicts / ANTHROPIC_AUTH_TOKEN]")
	assertNotContains(t, output, "[IDE Config / ANTHROPIC_AUTH_TOKEN]")
	assertNotContains(t, accessibleInitialSummary(output), "\x1b[")
	assertNoSensitiveDiagnosticsLeak(t, output, leakedToken, testToken)
	if got := strings.Count(output, "Gateway: token rejected"); got != 1 {
		t.Fatalf("primary gateway finding count = %d, want 1\n%s", got, output)
	}
	if got := strings.Count(output, "Config: ANTHROPIC_AUTH_TOKEN differs across sources"); got != 1 {
		t.Fatalf("grouped config finding count = %d, want 1\n%s", got, output)
	}
	if got := strings.Count(output, "IDE: ANTHROPIC_AUTH_TOKEN differs from current environment"); got != 1 {
		t.Fatalf("grouped IDE finding count = %d, want 1\n%s", got, output)
	}
	if h.fs.mutationOps() != 0 {
		t.Fatalf("diagnostic summary wrote filesystem %d time(s)\n%s", h.fs.mutationOps(), output)
	}
}

func TestAccessibleWizardReviewDetailsRedactsSensitiveGatewayEvidence(t *testing.T) {
	h := newHarness(t, withGatewayOptions(t, fakeGatewayOptions{
		acceptedTokens: []string{leakedToken},
		listResponses: []gatewayResponse{{
			status: http.StatusBadGateway,
			body:   `{"detail":"Bearer ` + leakedToken + ` api_key=` + leakedToken + ` from /home/ada/.cache/llmgate"}`,
		}},
	}))
	rawBaseURL := h.gateway.url() + "/v1/models?api_key=" + leakedToken + "#fragment"
	addAccessibleFile(h.fs, "/home/ada/.claude/settings.json", []byte(`{"env":{`+
		`"ANTHROPIC_AUTH_TOKEN":"`+leakedToken+`",`+
		`"ANTHROPIC_BASE_URL":"`+rawBaseURL+`",`+
		`"ANTHROPIC_MODEL":"`+sonnetModel+`",`+
		`"ANTHROPIC_DEFAULT_HAIKU_MODEL":"`+haikuModel+`",`+
		`"ANTHROPIC_DEFAULT_SONNET_MODEL":"`+sonnetModel+`",`+
		`"ANTHROPIC_DEFAULT_OPUS_MODEL":"`+opusModel+`"`+
		`}}`+"\n"), 0o600)

	output, err := h.runAccessible("y\n2\n")
	if err != nil {
		t.Fatalf("Run() error = %v\n%s", err, output)
	}

	assertContains(t, output, "Initial diagnostics: FAIL")
	assertContains(t, output, "llmgate diagnosis: FAIL")
	assertContains(t, output, "gateway validation failed")
	assertContains(t, output, "reason: model list failed: http HTTP 502")
	assertContains(t, output, "sk-...7890")
	assertContains(t, output, "~/.cache/llmgate")
	assertNoSensitiveDiagnosticsLeak(t, output, leakedToken)
	if h.fs.mutationOps() != 0 {
		t.Fatalf("review details mutated filesystem %d time(s)\n%s", h.fs.mutationOps(), output)
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

func addAccessibleFile(f *trackingFS, path string, data []byte, mode fs.FileMode) {
	f.files[path] = append([]byte(nil), data...)
	f.modes[path] = mode
	f.addDir(parentDir(path))
}

func accessibleInitialSummary(output string) string {
	start := strings.Index(output, "Initial diagnostics:")
	if start < 0 {
		return output
	}
	var summary strings.Builder
	for _, line := range strings.Split(output[start:], "\n") {
		if strings.HasPrefix(line, "\x1b[") || strings.Contains(line, "llmgate diagnosis:") {
			break
		}
		summary.WriteString(line)
		summary.WriteByte('\n')
	}
	return summary.String()
}

func assertNoSensitiveDiagnosticsLeak(t *testing.T, output string, secrets ...string) {
	t.Helper()
	assertNoSecretLeak(t, output, secrets...)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		assertNotContains(t, output, "Bearer "+secret)
		assertNotContains(t, output, "api_key="+secret)
	}
	assertNotContains(t, output, "/home/ada")
}
