package wizard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/xpty"
	"github.com/r13v/llmgate/internal/config"
	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/diagnose"
	"github.com/r13v/llmgate/internal/gateway"
	"github.com/r13v/llmgate/internal/system"
)

func TestAccessibleStartupDeclinePerformsNoReads(t *testing.T) {
	var output bytes.Buffer
	sys := testSystem(panicFileSystem{}, map[string]string{"SHELL": "/bin/zsh"})
	sys.Terminal = testTerminal{interactive: true}

	err := Run(context.Background(), Options{
		System: sys,
		Prompter: HuhPrompter{
			In:         newOneByteReader("n\n"),
			Output:     &output,
			Accessible: true,
		},
		Output: &output,
	})

	if !errors.Is(err, ErrStartupDeclined) {
		t.Fatalf("Run() error = %v, want ErrStartupDeclined", err)
	}
	if !strings.Contains(output.String(), "No files were read or changed.") {
		t.Fatalf("decline output missing no-read message:\n%s", output.String())
	}
}

func TestAccessibleFreshSetupWritesSelectedTargetsAndRedactsToken(t *testing.T) {
	server := newWizardGateway(t, []string{"claude-haiku-4", "claude-sonnet-4", "claude-opus-4"}, nil)
	defer server.Close()

	fileSystem := newWizardFileSystem()
	var output bytes.Buffer
	token := "sk-goodtoken1234567890"
	input := strings.Join([]string{
		"y",
		"1",
		token,
		server.URL,
		"y",
		"0",
		"y",
		"",
	}, "\n")

	err := Run(context.Background(), Options{
		System:  testSystem(fileSystem, map[string]string{"SHELL": "/bin/zsh"}),
		Gateway: testGateway(server),
		Prompter: HuhPrompter{
			In:         newOneByteReader(input),
			Output:     &output,
			Accessible: true,
		},
		Output: &output,
	})
	if err != nil {
		t.Fatalf("Run() error = %v\n%s", err, output.String())
	}

	assertFileContains(t, fileSystem, "/home/ada/.claude/settings.json", token)
	assertFileContains(t, fileSystem, "/home/ada/.zshrc", "export ANTHROPIC_MODEL='claude-sonnet-4'")
	rendered := output.String()
	if strings.Contains(rendered, token) {
		t.Fatalf("wizard output leaked token:\n%s", rendered)
	}
	assertContains(t, rendered, "Configured")
	assertContains(t, rendered, "Restart your terminal and IDE")
}

func TestModelPromptsRedactTokensInGatewayModelIDs(t *testing.T) {
	token := "sk-goodtoken1234567890"
	model := "claude-sonnet-" + token
	display := displayOptions{HomeDir: "/home/ada", GOOS: "linux"}
	prompts := &recordingPrompter{
		confirmResponses: []bool{false, false},
		selectResponses:  []string{model},
	}

	useRecommendation, err := promptUseRecommendation(context.Background(), prompts, gateway.Recommendation{
		Primary: model,
		Haiku:   model,
		Sonnet:  model,
		Opus:    model,
	}, token, display)
	if err != nil {
		t.Fatalf("promptUseRecommendation() error = %v", err)
	}
	if useRecommendation {
		t.Fatalf("useRecommendation = true, want false")
	}
	values, err := promptManualModels(context.Background(), prompts, []string{model}, core.SetupValues{
		AuthToken: token,
		BaseURL:   "https://gateway.example.com",
	}, gateway.Recommendation{}, display)
	if err != nil {
		t.Fatalf("promptManualModels() error = %v", err)
	}
	if values.Model != model {
		t.Fatalf("selected model = %q, want raw model value %q", values.Model, model)
	}

	renderedPrompts := strings.Join(prompts.descriptions, "\n") + "\n" + strings.Join(prompts.optionLabels, "\n")
	assertNotContains(t, renderedPrompts, token)
	assertContains(t, renderedPrompts, "sk-...7890")
}

func TestCredentialPromptRedactsAndCanonicalizesExistingBaseURL(t *testing.T) {
	token := "sk-goodtoken1234567890"
	rawBaseURL := "https://" + token + "@gateway.example.com/proxy/v1/models?api_key=" + token + "#fragment"
	prompts := &credentialPrompter{confirmResponses: []bool{true}}

	gotToken, gotBaseURL, err := promptCredentials(context.Background(), prompts, credentialDefaults{
		Token:       token,
		TokenFound:  true,
		TokenSource: core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"},
		BaseURL:     rawBaseURL,
		BaseSource:  core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"},
	}, displayOptions{HomeDir: "/home/ada", GOOS: "linux"})
	if err != nil {
		t.Fatalf("promptCredentials() error = %v", err)
	}
	if gotToken != token {
		t.Fatalf("token = %q, want existing token", gotToken)
	}
	if gotBaseURL != "https://gateway.example.com/proxy/v1" {
		t.Fatalf("base URL = %q, want canonical URL", gotBaseURL)
	}

	renderedPrompts := strings.Join(prompts.descriptions, "\n") + "\n" + strings.Join(prompts.inputDefaults, "\n")
	assertNotContains(t, renderedPrompts, token)
	assertNotContains(t, renderedPrompts, rawBaseURL)
	assertContains(t, renderedPrompts, "https://gateway.example.com/proxy/v1")
}

func TestCredentialPromptUsesPlaceholderNotDefaultForMissingBaseURL(t *testing.T) {
	token := "sk-goodtoken1234567890"
	baseURL := "https://gateway.example.com"
	prompts := &credentialPrompter{inputResponses: []string{token, baseURL}}

	gotToken, gotBaseURL, err := promptCredentials(context.Background(), prompts, credentialDefaults{}, displayOptions{HomeDir: "/home/ada", GOOS: "linux"})
	if err != nil {
		t.Fatalf("promptCredentials() error = %v", err)
	}
	if gotToken != token {
		t.Fatalf("token = %q, want prompted token", gotToken)
	}
	if gotBaseURL != baseURL {
		t.Fatalf("base URL = %q, want prompted base URL", gotBaseURL)
	}
	if len(prompts.inputDefaults) != 2 {
		t.Fatalf("input defaults = %#v, want token and base URL prompts", prompts.inputDefaults)
	}
	if prompts.inputDefaults[1] != "" {
		t.Fatalf("base URL default = %q, want empty", prompts.inputDefaults[1])
	}
	if prompts.inputPlaceholders[1] != core.DefaultBaseURLPlaceholder {
		t.Fatalf("base URL placeholder = %q, want %q", prompts.inputPlaceholders[1], core.DefaultBaseURLPlaceholder)
	}
}

func TestDiagnosticSummaryRedactsSecretsAndHomePaths(t *testing.T) {
	var output bytes.Buffer
	token := "plain-secret-token"
	result := diagnose.Result{
		Read: configReadForSummary(token),
		Sections: []core.DiagnosticSection{{
			Title: "Model Probes",
			Checks: []core.DiagnosticCheck{{
				Status:  core.StatusWARN,
				Title:   "Probe",
				Summary: `probe failed for model "claude-` + token + `" at /home/ada/.claude/settings.json`,
				Details: []string{`reason: model probe failed for sk-` + token + ` in /home/ada/.claude/settings.json`},
			}},
		}},
	}

	runner{out: &output}.printDiagnosticSummary("Initial diagnostics", result)

	rendered := output.String()
	assertNotContains(t, rendered, token)
	assertNotContains(t, rendered, "/home/ada")
	assertContains(t, rendered, "~/")
	assertContains(t, rendered, "- WARN: probe failed for model")
	assertNotContains(t, rendered, "[Model Probes / Probe]")
	assertNotContains(t, rendered, "reason: model probe failed")
}

func TestDiagnosticSummaryRendersFindingsBeforeUncoveredChecks(t *testing.T) {
	var output bytes.Buffer
	token := "sk-summary-secret-1234567890"
	result := diagnose.Result{
		Read: configReadForSummary(token),
		Sections: []core.DiagnosticSection{
			{
				Title: "Gateway",
				Checks: []core.DiagnosticCheck{{
					ID:      "gateway.validation",
					Status:  core.StatusFAIL,
					Title:   "Gateway validation",
					Summary: "gateway validation failed",
					Details: []string{"raw gateway detail for " + token},
				}},
			},
			{
				Title: "Claude Code CLI",
				Checks: []core.DiagnosticCheck{{
					ID:      "claude-cli.version",
					Status:  core.StatusWARN,
					Title:   "claude --version",
					Summary: "Claude Code CLI check failed",
					Details: []string{"exit status 127"},
				}},
			},
			{
				Title: "Config Sources",
				Checks: []core.DiagnosticCheck{{
					ID:      "config-sources.01",
					Status:  core.StatusWARN,
					Title:   "Malformed source",
					Summary: "Failed to parse /home/ada/.claude/settings.json",
					Details: []string{"raw value: " + token},
				}},
			},
			{
				Title: "Project Overrides",
				Checks: []core.DiagnosticCheck{{
					ID:      "project-overrides.01",
					Status:  core.StatusWARN,
					Title:   "Project override",
					Summary: "Project override differs from terminal config",
					Details: []string{"path: /home/ada/project/.claude/settings.local.json"},
				}},
			},
		},
		Findings: []core.DiagnosticFinding{{
			ID:      "gateway.current",
			Status:  core.StatusFAIL,
			Title:   "Gateway: token rejected",
			Summary: "The gateway rejected ANTHROPIC_AUTH_TOKEN.",
			Evidence: []string{
				"request URL: https://gateway.example.com/v1/models?api_key=" + token,
				"HTTP status: 401",
				"gateway message: gateway rejected token " + token + " " + strings.Repeat("x", 240),
			},
			Remediation:   "Update the active token in ~/.zshrc or choose one source of truth.",
			RelatedChecks: []string{"gateway.validation"},
		}},
	}

	runner{out: &output}.printDiagnosticSummary("Initial diagnostics", result)

	rendered := output.String()
	assertContains(t, rendered, "Initial diagnostics: FAIL")
	assertContains(t, rendered, "- FAIL Gateway: token rejected")
	assertContains(t, rendered, "why: The gateway rejected ANTHROPIC_AUTH_TOKEN.")
	assertContains(t, rendered, "evidence: request URL: https://gateway.example.com/v1/models")
	assertContains(t, rendered, "evidence: HTTP status: 401")
	assertContains(t, rendered, "fix: Update the active token")
	assertContains(t, rendered, "- WARN [Claude Code CLI / claude --version] Claude Code CLI check failed")
	assertContains(t, rendered, "exit status 127")
	assertContains(t, rendered, "- WARN [Config Sources / Malformed source] Failed to parse ~/.claude/settings.json")
	assertContains(t, rendered, "- WARN [Project Overrides / Project override] Project override differs from terminal config")
	assertNotContains(t, rendered, "gateway validation failed")
	assertNotContains(t, rendered, "raw gateway detail")
	assertNotContains(t, rendered, token)

	if strings.Index(rendered, "Gateway: token rejected") > strings.Index(rendered, "Claude Code CLI check failed") {
		t.Fatalf("finding rendered after uncovered check:\n%s", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "gateway message:") && len(line) > 200 {
			t.Fatalf("gateway evidence line too long (%d chars): %q", len(line), line)
		}
	}
}

func TestDiagnosticSummaryOmitsLongGatewayDetailThatReviewDetailsKeeps(t *testing.T) {
	var output bytes.Buffer
	token := "sk-longdetail1234567890"
	longDetail := "Key Hash abc123 LiteLLM_VerificationTokenTable gateway rejected token " + token + " from /home/ada/.cache/llmgate " +
		strings.Repeat("diagnostic context ", 20) + "LiteLLM_VerificationTokenTable"
	gatewayErr := &gateway.Error{
		Kind:       gateway.FailureAuth,
		Operation:  "model list",
		StatusCode: http.StatusUnauthorized,
		URL:        "https://gateway.example.com/v1/models",
		Detail:     longDetail,
	}
	explanation := gateway.ExplainFailure(gatewayErr)
	result := diagnose.Result{
		Read: configReadForSummary(token),
		Sections: []core.DiagnosticSection{{
			Title: "Gateway",
			Checks: []core.DiagnosticCheck{{
				ID:      "gateway.validation",
				Title:   "Gateway validation",
				Status:  core.StatusFAIL,
				Summary: "gateway validation failed",
				Details: []string{"reason: " + gatewayErr.Error()},
			}},
		}},
		Findings: []core.DiagnosticFinding{{
			ID:            "gateway.current",
			Status:        core.StatusFAIL,
			Title:         "Gateway: token rejected",
			Summary:       explanation.Cause,
			Evidence:      explanation.Evidence,
			Remediation:   explanation.Remediation,
			RelatedChecks: []string{"gateway.validation"},
		}},
	}

	runner{out: &output}.printDiagnosticSummary("Initial diagnostics", result)
	summary := output.String()
	reviewDetails := diagnose.Render(result, diagnose.RenderOptions{})

	assertContains(t, summary, "- FAIL Gateway: token rejected")
	assertContains(t, summary, "gateway message:")
	assertNotContains(t, summary, "LiteLLM_VerificationTokenTable")
	assertNotContains(t, summary, token)
	assertContains(t, reviewDetails, "LiteLLM_VerificationTokenTable")
	assertContains(t, reviewDetails, "sk-...7890")
	assertContains(t, reviewDetails, "~/.cache/llmgate")
	assertNotContains(t, reviewDetails, token)
}

func TestDiagnosticSummaryRedactsGatewayEvidenceBeforeTruncating(t *testing.T) {
	var output bytes.Buffer
	token := "plain-secret-token-1234567890"
	detail := strings.Repeat("x", 170) + token + " tail"
	explanation := gateway.ExplainFailure(&gateway.Error{
		Kind:       gateway.FailureHTTP,
		Operation:  "model list",
		StatusCode: http.StatusBadGateway,
		URL:        "https://gateway.example.com/v1/models",
		Detail:     detail,
	})
	result := diagnose.Result{
		Read: configReadForSummary(token),
		Findings: []core.DiagnosticFinding{{
			ID:          "gateway.current",
			Status:      core.StatusFAIL,
			Title:       "Gateway: HTTP request failed",
			Summary:     explanation.Cause,
			Evidence:    explanation.Evidence,
			Remediation: explanation.Remediation,
		}},
	}

	runner{out: &output}.printDiagnosticSummary("Initial diagnostics", result)

	rendered := output.String()
	assertNotContains(t, rendered, token)
	assertNotContains(t, rendered, token[:7])
}

func TestDiagnosticSummaryRedactsAllFindingFields(t *testing.T) {
	var output bytes.Buffer
	token := "sk-findingsecret1234567890"
	result := diagnose.Result{
		Read: configReadForSummary(token),
		Findings: []core.DiagnosticFinding{{
			ID:          "gateway.current",
			Status:      core.StatusFAIL,
			Title:       "Gateway: token rejected " + token + " at /home/ada/.claude/settings.json",
			Summary:     "Cause references " + token + " at /home/ada/.claude/settings.json",
			Evidence:    []string{"request URL: https://gateway.example.com/v1/models?api_key=" + token, "path: /home/ada/.cache/llmgate"},
			Remediation: "Update " + token + " in /home/ada/.zshrc",
		}},
	}

	runner{out: &output}.printDiagnosticSummary("Initial diagnostics", result)

	rendered := output.String()
	assertNotContains(t, rendered, token)
	assertNotContains(t, rendered, "/home/ada")
	assertContains(t, rendered, "sk-...7890")
	assertContains(t, rendered, "~/.claude/settings.json")
	assertContains(t, rendered, "~/.cache/llmgate")
	assertContains(t, rendered, "~/.zshrc")
}

func TestDiagnosticSummaryOmitsNoisyFindingLinesForOKDiagnostics(t *testing.T) {
	var output bytes.Buffer
	result := diagnose.Result{
		Read: configReadForSummary("sk-oksecret1234567890"),
		Sections: []core.DiagnosticSection{{
			Title: "Gateway",
			Checks: []core.DiagnosticCheck{{
				ID:      "gateway.validation",
				Status:  core.StatusOK,
				Title:   "Gateway validation",
				Summary: "gateway model list succeeded",
			}},
		}},
		Findings: []core.DiagnosticFinding{{
			ID:      "gateway.current",
			Status:  core.StatusOK,
			Title:   "Gateway: healthy",
			Summary: "The gateway is healthy.",
		}},
	}

	runner{out: &output}.printDiagnosticSummary("Initial diagnostics", result)

	rendered := output.String()
	if rendered != "Initial diagnostics: OK\n" {
		t.Fatalf("OK summary = %q, want quiet status line only", rendered)
	}
	assertNotContains(t, rendered, "why:")
	assertNotContains(t, rendered, "evidence:")
	assertNotContains(t, rendered, "fix:")
	assertNotContains(t, rendered, "Gateway: healthy")
}

func TestDiagnosticSummaryColorsOnlyStatusTokens(t *testing.T) {
	var output bytes.Buffer
	result := diagnose.Result{
		Read: configReadForSummary("sk-color-secret-1234567890"),
		Sections: []core.DiagnosticSection{{
			Title: "Gateway",
			Checks: []core.DiagnosticCheck{{
				ID:      "gateway.validation",
				Status:  core.StatusFAIL,
				Title:   "Gateway validation",
				Summary: "gateway validation failed",
			}},
		}},
		Findings: []core.DiagnosticFinding{{
			ID:            "gateway.current",
			Status:        core.StatusFAIL,
			Title:         "Gateway: token rejected",
			Summary:       "The gateway rejected ANTHROPIC_AUTH_TOKEN.",
			RelatedChecks: []string{"gateway.validation"},
		}},
	}

	runner{out: &output, color: true}.printDiagnosticSummary("Initial diagnostics", result)

	rendered := output.String()
	assertContains(t, rendered, "Initial diagnostics: \x1b[31mFAIL\x1b[0m")
	assertContains(t, rendered, "- \x1b[31mFAIL\x1b[0m Gateway: token rejected")
	assertNotContains(t, rendered, "\x1b[31mGateway")
	assertNotContains(t, rendered, "\x1b[31mThe gateway")
}

func TestGatewayRecoveryPromptUsesConciseRedactedGatewayExplanation(t *testing.T) {
	token := "sk-promptsecret1234567890"
	longDetail := "gateway rejected token " + token + " " +
		strings.Repeat("diagnostic context ", 20) + "LiteLLM_VerificationTokenTable"
	prompts := &recordingPrompter{selectResponses: []string{string(gatewayRecoveryEdit)}}

	got, err := promptGatewayRecovery(context.Background(), prompts, &gateway.Error{
		Kind:       gateway.FailureAuth,
		Operation:  "model list",
		StatusCode: http.StatusUnauthorized,
		URL:        "https://gateway.example.com/v1/models?api_key=" + token,
		Detail:     longDetail,
	}, token, displayOptions{HomeDir: "/home/ada", GOOS: "linux"})
	if err != nil {
		t.Fatalf("promptGatewayRecovery() error = %v", err)
	}
	if got != gatewayRecoveryEdit {
		t.Fatalf("recovery = %q, want edit", got)
	}

	description := strings.Join(prompts.descriptions, "\n")
	assertContains(t, description, "Cause: The gateway rejected the configured ANTHROPIC_AUTH_TOKEN.")
	assertContains(t, description, "Evidence: request URL: https://gateway.example.com/v1/models")
	assertContains(t, description, "Evidence: HTTP status: 401")
	assertContains(t, description, "Fix: Update ANTHROPIC_AUTH_TOKEN")
	assertNotContains(t, description, "LiteLLM_VerificationTokenTable")
	assertNotContains(t, description, token)
}

func TestProgressReporterIsSilentForNonTerminalOutput(t *testing.T) {
	var output bytes.Buffer
	reporter := newProgressReporter(&output, &testEnvironment{values: map[string]string{"TERM": "xterm-256color"}}, false)
	if reporter.animate || reporter.log || reporter.color {
		t.Fatalf("non-terminal progress flags = animate:%t log:%t color:%t, want all false", reporter.animate, reporter.log, reporter.color)
	}

	called := false
	err := reporter.Run("Checking gateway model list.", func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !called {
		t.Fatalf("Run() did not call wrapped function")
	}
	if got := output.String(); got != "" {
		t.Fatalf("non-terminal progress wrote output %q, want silence", got)
	}
}

func TestProgressReporterEnablesStatusOutputOnlyForTTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix PTY smoke test is skipped on Windows")
	}
	pty, err := xpty.NewUnixPty(80, 24)
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	defer func() {
		_ = pty.Close()
	}()

	env := &testEnvironment{values: map[string]string{"TERM": "xterm-256color"}}
	reporter := newProgressReporter(pty.Slave(), env, false)
	if !reporter.animate || !reporter.log || !reporter.color {
		t.Fatalf("TTY progress flags = animate:%t log:%t color:%t, want all true", reporter.animate, reporter.log, reporter.color)
	}

	accessible := newProgressReporter(pty.Slave(), env, true)
	if accessible.animate || !accessible.log || accessible.color {
		t.Fatalf("accessible TTY progress flags = animate:%t log:%t color:%t, want animate false, log true, color false", accessible.animate, accessible.log, accessible.color)
	}

	dumb := newProgressReporter(pty.Slave(), &testEnvironment{values: map[string]string{"TERM": "dumb"}}, false)
	if dumb.animate || !dumb.log || dumb.color {
		t.Fatalf("dumb TTY progress flags = animate:%t log:%t color:%t, want animate false, log true, color false", dumb.animate, dumb.log, dumb.color)
	}
}

func TestGatewayProgressMessagesSanitizeSecrets(t *testing.T) {
	token := "sk-goodtoken1234567890"
	rawBaseURL := "https://" + token + "@gateway.example.com/proxy/v1/models?api_key=" + token + "#fragment"
	display := displayOptions{HomeDir: "/home/ada", GOOS: "linux"}

	rendered := gatewayModelListProgressMessage(rawBaseURL, token, display) + "\n" +
		gatewayProbeProgressMessage(rawBaseURL, token, "claude-"+token, display)

	assertNotContains(t, rendered, token)
	assertNotContains(t, rendered, rawBaseURL)
	assertContains(t, rendered, "https://gateway.example.com/proxy/v1/models")
	assertContains(t, rendered, "https://gateway.example.com/proxy/v1/chat/completions")
	assertContains(t, rendered, "claude-sk-...7890")
}

func TestAccessibleGatewayRetryAndRejectedPlanReturnToTargets(t *testing.T) {
	var listAttempts int
	server := newWizardGateway(t, []string{"claude-haiku-4", "claude-sonnet-4", "claude-opus-4"}, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/v1/models" {
			listAttempts++
			if listAttempts == 1 {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"detail":"temporary failure"}`))
				return true
			}
		}
		return false
	})
	defer server.Close()

	fileSystem := newWizardFileSystem()
	var output bytes.Buffer
	input := strings.Join([]string{
		"y",
		"1",
		"sk-goodtoken1234567890",
		server.URL,
		"2",
		"y",
		"0",
		"n",
		"0",
		"y",
		"",
	}, "\n")

	err := Run(context.Background(), Options{
		System:  testSystem(fileSystem, map[string]string{"SHELL": "/bin/zsh"}),
		Gateway: testGateway(server),
		Prompter: HuhPrompter{
			In:         newOneByteReader(input),
			Output:     &output,
			Accessible: true,
		},
		Output: &output,
	})
	if err != nil {
		t.Fatalf("Run() error = %v\n%s", err, output.String())
	}
	if listAttempts < 2 {
		t.Fatalf("gateway list attempts = %d, want retry", listAttempts)
	}
	if got := strings.Count(output.String(), "Apply plan: setup gateway credentials"); got < 2 {
		t.Fatalf("apply plan rendered %d time(s), want rejected plan to return to targets\n%s", got, output.String())
	}
	assertFileContains(t, fileSystem, "/home/ada/.claude/settings.json", "sk-goodtoken1234567890")
}

func TestSetupPersistsCanonicalGatewayBaseURL(t *testing.T) {
	server := newWizardGateway(t, []string{"claude-haiku-4", "claude-sonnet-4", "claude-opus-4"}, nil)
	defer server.Close()

	fileSystem := newWizardFileSystem()
	rawBaseURL := server.URL + "/v1/models?ignored=1#fragment"
	prompts := &scriptedPrompter{responses: []promptResponse{
		{kind: "confirm", confirm: true},
		{kind: "select", value: string(actionSetup)},
		{kind: "input", value: "sk-goodtoken1234567890"},
		{kind: "input", value: rawBaseURL},
		{kind: "confirm", confirm: true},
		{kind: "multiselect", values: []string{"0", "1"}},
		{kind: "confirm", confirm: true},
	}}
	var output bytes.Buffer

	err := Run(context.Background(), Options{
		System:   testSystem(fileSystem, map[string]string{"SHELL": "/bin/zsh"}),
		Gateway:  testGateway(server),
		Prompter: prompts,
		Output:   &output,
	})
	if err != nil {
		t.Fatalf("Run() error = %v\n%s", err, output.String())
	}

	settings := string(fileSystem.files["/home/ada/.claude/settings.json"])
	assertContains(t, settings, server.URL+`/v1"`)
	assertNotContains(t, settings, rawBaseURL)
	assertNotContains(t, settings, "ignored=1")
	assertFileContains(t, fileSystem, "/home/ada/.zshrc", "ANTHROPIC_BASE_URL='"+server.URL+"/v1'")
}

func TestScriptedPromptCancellationsExitBeforeWrites(t *testing.T) {
	cases := []struct {
		name      string
		server    *httptest.Server
		existing  bool
		responses []promptResponse
		wantErr   error
	}{
		{
			name:      "startup",
			responses: []promptResponse{{kind: "confirm", err: ErrCanceled}},
			wantErr:   ErrStartupDeclined,
		},
		{
			name: "action",
			responses: []promptResponse{
				{kind: "confirm", confirm: true},
				{kind: "select", err: ErrCanceled},
			},
		},
		{
			name:     "existing token reuse",
			existing: true,
			responses: []promptResponse{
				{kind: "confirm", confirm: true},
				{kind: "select", value: string(actionSetup)},
				{kind: "confirm", err: ErrCanceled},
			},
		},
		{
			name: "token input",
			responses: []promptResponse{
				{kind: "confirm", confirm: true},
				{kind: "select", value: string(actionSetup)},
				{kind: "input", err: ErrCanceled},
			},
		},
		{
			name: "base URL input",
			responses: []promptResponse{
				{kind: "confirm", confirm: true},
				{kind: "select", value: string(actionSetup)},
				{kind: "input", value: "sk-goodtoken1234567890"},
				{kind: "input", err: ErrCanceled},
			},
		},
		{
			name:   "gateway recovery",
			server: failingModelListGateway(t),
			responses: []promptResponse{
				{kind: "confirm", confirm: true},
				{kind: "select", value: string(actionSetup)},
				{kind: "input", value: "sk-goodtoken1234567890"},
				{kind: "input", value: "http://example.test"},
				{kind: "select", err: ErrCanceled},
			},
		},
		{
			name: "recommendation",
			responses: []promptResponse{
				{kind: "confirm", confirm: true},
				{kind: "select", value: string(actionSetup)},
				{kind: "input", value: "sk-goodtoken1234567890"},
				{kind: "input", value: "SERVER"},
				{kind: "confirm", err: ErrCanceled},
			},
		},
		{
			name: "manual primary model",
			responses: []promptResponse{
				{kind: "confirm", confirm: true},
				{kind: "select", value: string(actionSetup)},
				{kind: "input", value: "sk-goodtoken1234567890"},
				{kind: "input", value: "SERVER"},
				{kind: "confirm", confirm: false},
				{kind: "select", err: ErrCanceled},
			},
		},
		{
			name: "advanced override prompt",
			responses: []promptResponse{
				{kind: "confirm", confirm: true},
				{kind: "select", value: string(actionSetup)},
				{kind: "input", value: "sk-goodtoken1234567890"},
				{kind: "input", value: "SERVER"},
				{kind: "confirm", confirm: false},
				{kind: "select", value: "claude-sonnet-4"},
				{kind: "confirm", err: ErrCanceled},
			},
		},
		{
			name: "haiku override model",
			responses: []promptResponse{
				{kind: "confirm", confirm: true},
				{kind: "select", value: string(actionSetup)},
				{kind: "input", value: "sk-goodtoken1234567890"},
				{kind: "input", value: "SERVER"},
				{kind: "confirm", confirm: false},
				{kind: "select", value: "claude-sonnet-4"},
				{kind: "confirm", confirm: true},
				{kind: "select", err: ErrCanceled},
			},
		},
		{
			name: "sonnet override model",
			responses: []promptResponse{
				{kind: "confirm", confirm: true},
				{kind: "select", value: string(actionSetup)},
				{kind: "input", value: "sk-goodtoken1234567890"},
				{kind: "input", value: "SERVER"},
				{kind: "confirm", confirm: false},
				{kind: "select", value: "claude-sonnet-4"},
				{kind: "confirm", confirm: true},
				{kind: "select", value: "claude-haiku-4"},
				{kind: "select", err: ErrCanceled},
			},
		},
		{
			name: "opus override model",
			responses: []promptResponse{
				{kind: "confirm", confirm: true},
				{kind: "select", value: string(actionSetup)},
				{kind: "input", value: "sk-goodtoken1234567890"},
				{kind: "input", value: "SERVER"},
				{kind: "confirm", confirm: false},
				{kind: "select", value: "claude-sonnet-4"},
				{kind: "confirm", confirm: true},
				{kind: "select", value: "claude-haiku-4"},
				{kind: "select", value: "claude-sonnet-4"},
				{kind: "select", err: ErrCanceled},
			},
		},
		{
			name:   "probe recovery",
			server: newWizardGateway(t, []string{"claude-haiku-4", "claude-sonnet-probe-fail", "claude-opus-4"}, nil),
			responses: []promptResponse{
				{kind: "confirm", confirm: true},
				{kind: "select", value: string(actionSetup)},
				{kind: "input", value: "sk-goodtoken1234567890"},
				{kind: "input", value: "SERVER"},
				{kind: "confirm", confirm: true},
				{kind: "select", err: ErrCanceled},
			},
		},
		{
			name: "target selection",
			responses: []promptResponse{
				{kind: "confirm", confirm: true},
				{kind: "select", value: string(actionSetup)},
				{kind: "input", value: "sk-goodtoken1234567890"},
				{kind: "input", value: "SERVER"},
				{kind: "confirm", confirm: true},
				{kind: "multiselect", err: ErrCanceled},
			},
		},
		{
			name: "apply confirmation",
			responses: []promptResponse{
				{kind: "confirm", confirm: true},
				{kind: "select", value: string(actionSetup)},
				{kind: "input", value: "sk-goodtoken1234567890"},
				{kind: "input", value: "SERVER"},
				{kind: "confirm", confirm: true},
				{kind: "multiselect", values: []string{"0", "1"}},
				{kind: "confirm", err: ErrCanceled},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := tc.server
			if server == nil {
				server = newWizardGateway(t, []string{"claude-haiku-4", "claude-sonnet-4", "claude-opus-4"}, nil)
				defer server.Close()
			} else {
				defer server.Close()
			}
			responses := replaceServerURL(tc.responses, server.URL)
			fileSystem := newWizardFileSystem()
			if tc.existing {
				fileSystem.addFile("/home/ada/.claude/settings.json", []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-existing1234","ANTHROPIC_BASE_URL":"`+server.URL+`"}}`+"\n"), 0o600)
			}
			prompts := &scriptedPrompter{responses: responses}
			var output bytes.Buffer

			err := Run(context.Background(), Options{
				System:   testSystem(fileSystem, map[string]string{"SHELL": "/bin/zsh"}),
				Gateway:  testGateway(server),
				Prompter: prompts,
				Output:   &output,
			})
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("Run() error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && err != nil {
				t.Fatalf("Run() error = %v\n%s", err, output.String())
			}
			if fileSystem.writes != 0 {
				t.Fatalf("cancellation wrote filesystem %d time(s); output:\n%s", fileSystem.writes, output.String())
			}
		})
	}
}

func replaceServerURL(responses []promptResponse, serverURL string) []promptResponse {
	out := make([]promptResponse, len(responses))
	copy(out, responses)
	for i := range out {
		if out[i].value == "SERVER" {
			out[i].value = serverURL
		}
	}
	return out
}

func testGateway(server *httptest.Server) gateway.Client {
	return gateway.Client{HTTPClient: server.Client(), Cache: gateway.NewCache()}
}

func configReadForSummary(token string) config.ReadResult {
	label := core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"}
	return config.ReadResult{
		Paths: system.DiscoveredPaths{HomeDir: "/home/ada", GOOS: "linux"},
		Sources: []config.Source{{
			Label: label,
			Values: map[string]core.ConfigValue{
				core.VarAnthropicAuthToken: {
					Name:   core.VarAnthropicAuthToken,
					Value:  token,
					Source: label,
					Secret: true,
				},
			},
		}},
	}
}

func newWizardGateway(t *testing.T, models []string, hook func(http.ResponseWriter, *http.Request) bool) *httptest.Server {
	t.Helper()
	modelSet := make(map[string]bool, len(models))
	for _, model := range models {
		modelSet[model] = true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if hook != nil && hook(w, r) {
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-goodtoken1234567890" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":"gateway rejected token"}`))
			return
		}
		data := make([]map[string]string, 0, len(models))
		for _, model := range models {
			data = append(data, map[string]string{"id": model})
		}
		writeJSON(t, w, map[string]any{"data": data})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if hook != nil && hook(w, r) {
			return
		}
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode probe payload: %v", err)
		}
		if !modelSet[payload.Model] || strings.Contains(payload.Model, "probe-fail") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":"model unavailable"}`))
			return
		}
		writeJSON(t, w, map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": ""}}}})
	})
	return httptest.NewServer(mux)
}

func failingModelListGateway(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"detail":"bad gateway"}`))
	})
	return httptest.NewServer(mux)
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func testSystem(fileSystem system.FileSystem, env map[string]string) system.System {
	return system.System{
		FS: fileSystem,
		Env: &testEnvironment{
			values: env,
		},
		Commands: &testCommandRunner{result: system.CommandResult{Stdout: "claude 1.0.0\n"}},
		Terminal: testTerminal{interactive: true},
		Platform: testPlatform{
			targetOS: "linux",
			home:     "/home/ada",
			work:     "/home/ada/project",
		},
		WindowsEnv: &testWindowsEnvironment{values: map[string]string{}},
	}
}

type promptResponse struct {
	kind    string
	confirm bool
	value   string
	values  []string
	err     error
}

type scriptedPrompter struct {
	responses []promptResponse
	index     int
}

type recordingPrompter struct {
	confirmResponses []bool
	selectResponses  []string
	descriptions     []string
	optionLabels     []string
}

type credentialPrompter struct {
	confirmResponses  []bool
	descriptions      []string
	inputDefaults     []string
	inputPlaceholders []string
	inputResponses    []string
}

func (p *recordingPrompter) Confirm(_ context.Context, prompt ConfirmPrompt) (bool, error) {
	p.descriptions = append(p.descriptions, prompt.Description)
	if len(p.confirmResponses) == 0 {
		return false, errors.New("unexpected confirm prompt")
	}
	value := p.confirmResponses[0]
	p.confirmResponses = p.confirmResponses[1:]
	return value, nil
}

func (p *recordingPrompter) Input(context.Context, InputPrompt) (string, error) {
	return "", errors.New("unexpected input prompt")
}

func (p *recordingPrompter) Select(_ context.Context, prompt SelectPrompt) (string, error) {
	p.descriptions = append(p.descriptions, prompt.Description)
	for _, option := range prompt.Options {
		p.optionLabels = append(p.optionLabels, option.Label)
	}
	if len(p.selectResponses) == 0 {
		return "", errors.New("unexpected select prompt")
	}
	value := p.selectResponses[0]
	p.selectResponses = p.selectResponses[1:]
	return value, nil
}

func (p *recordingPrompter) MultiSelect(context.Context, MultiSelectPrompt) ([]string, error) {
	return nil, errors.New("unexpected multiselect prompt")
}

func (p *credentialPrompter) Confirm(_ context.Context, prompt ConfirmPrompt) (bool, error) {
	p.descriptions = append(p.descriptions, prompt.Description)
	if len(p.confirmResponses) == 0 {
		return false, errors.New("unexpected confirm prompt")
	}
	value := p.confirmResponses[0]
	p.confirmResponses = p.confirmResponses[1:]
	return value, nil
}

func (p *credentialPrompter) Input(_ context.Context, prompt InputPrompt) (string, error) {
	p.descriptions = append(p.descriptions, prompt.Description)
	p.inputDefaults = append(p.inputDefaults, prompt.Default)
	p.inputPlaceholders = append(p.inputPlaceholders, prompt.Placeholder)
	if len(p.inputResponses) > 0 {
		value := p.inputResponses[0]
		p.inputResponses = p.inputResponses[1:]
		return value, nil
	}
	return prompt.Default, nil
}

func (p *credentialPrompter) Select(context.Context, SelectPrompt) (string, error) {
	return "", errors.New("unexpected select prompt")
}

func (p *credentialPrompter) MultiSelect(context.Context, MultiSelectPrompt) ([]string, error) {
	return nil, errors.New("unexpected multiselect prompt")
}

func (p *scriptedPrompter) Confirm(_ context.Context, _ ConfirmPrompt) (bool, error) {
	response := p.next("confirm")
	return response.confirm, response.err
}

func (p *scriptedPrompter) Input(_ context.Context, _ InputPrompt) (string, error) {
	response := p.next("input")
	return response.value, response.err
}

func (p *scriptedPrompter) Select(_ context.Context, _ SelectPrompt) (string, error) {
	response := p.next("select")
	return response.value, response.err
}

func (p *scriptedPrompter) MultiSelect(_ context.Context, _ MultiSelectPrompt) ([]string, error) {
	response := p.next("multiselect")
	return response.values, response.err
}

func (p *scriptedPrompter) next(kind string) promptResponse {
	if p.index >= len(p.responses) {
		return promptResponse{kind: kind, err: errors.New("unexpected prompt " + kind)}
	}
	response := p.responses[p.index]
	p.index++
	if response.kind != kind {
		return promptResponse{kind: kind, err: errors.New("unexpected prompt " + kind + ", want " + response.kind)}
	}
	return response
}

type testTerminal struct {
	interactive bool
}

type oneByteReader struct {
	reader *strings.Reader
}

func newOneByteReader(input string) *oneByteReader {
	return &oneByteReader{reader: strings.NewReader(input)}
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.reader.Read(p)
}

func (t testTerminal) IsInteractive() bool {
	return t.interactive
}

type testCommandRunner struct {
	result system.CommandResult
	err    error
}

func (r *testCommandRunner) Run(context.Context, string, ...string) (system.CommandResult, error) {
	return r.result, r.err
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
	values map[string]string
}

func (e *testWindowsEnvironment) Lookup(name string) (string, bool, error) {
	value, ok := e.values[name]
	return value, ok, nil
}

func (e *testWindowsEnvironment) Snapshot(names []string) (map[string]string, error) {
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

type wizardFileSystem struct {
	files  map[string][]byte
	modes  map[string]fs.FileMode
	dirs   map[string]bool
	writes int
}

func newWizardFileSystem() *wizardFileSystem {
	return &wizardFileSystem{
		files: make(map[string][]byte),
		modes: make(map[string]fs.FileMode),
		dirs:  map[string]bool{"/home/ada": true, "/home/ada/project": true},
	}
}

func (f *wizardFileSystem) addFile(path string, data []byte, mode fs.FileMode) {
	f.files[path] = append([]byte(nil), data...)
	f.modes[path] = mode
	f.addDir(parentDir(path))
}

func (f *wizardFileSystem) addDir(path string) {
	if path == "" || path == "." {
		return
	}
	f.dirs[path] = true
}

func (f *wizardFileSystem) ReadFile(path string) ([]byte, error) {
	data, ok := f.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}

func (f *wizardFileSystem) WriteFile(path string, data []byte, mode fs.FileMode) error {
	f.writes++
	f.files[path] = append([]byte(nil), data...)
	f.modes[path] = mode
	f.addDir(parentDir(path))
	return nil
}

func (f *wizardFileSystem) WriteFileExclusive(path string, data []byte, mode fs.FileMode) error {
	if _, ok := f.files[path]; ok {
		return fs.ErrExist
	}
	return f.WriteFile(path, data, mode)
}

func (f *wizardFileSystem) MkdirAll(path string, _ fs.FileMode) error {
	f.addDir(path)
	return nil
}

func (f *wizardFileSystem) Stat(path string) (fs.FileInfo, error) {
	if f.dirs[path] {
		return testFileInfo{name: path, mode: fs.ModeDir | 0o700, dir: true}, nil
	}
	data, ok := f.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return testFileInfo{name: path, size: int64(len(data)), mode: f.modes[path]}, nil
}

func (f *wizardFileSystem) Rename(oldPath, newPath string) error {
	f.writes++
	data, ok := f.files[oldPath]
	if !ok {
		return fs.ErrNotExist
	}
	f.files[newPath] = append([]byte(nil), data...)
	f.modes[newPath] = f.modes[oldPath]
	delete(f.files, oldPath)
	delete(f.modes, oldPath)
	return nil
}

func (f *wizardFileSystem) Remove(path string) error {
	f.writes++
	delete(f.files, path)
	delete(f.modes, path)
	return nil
}

func (f *wizardFileSystem) Chmod(path string, mode fs.FileMode) error {
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

func parentDir(path string) string {
	index := strings.LastIndexAny(path, `/\`)
	if index <= 0 {
		return "."
	}
	return path[:index]
}

func assertFileContains(t *testing.T, fileSystem *wizardFileSystem, path, want string) {
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

func assertNotContains(t *testing.T, got, notWant string) {
	t.Helper()
	if strings.Contains(got, notWant) {
		t.Fatalf("output unexpectedly contains %q:\n%s", notWant, got)
	}
}
