package diagnose

import (
	"context"
	"strings"
	"testing"

	"github.com/r13v/llmgate/internal/config"
	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/gateway"
	"github.com/r13v/llmgate/internal/system"
)

func TestRenderRedactsSecretsAndShortensHomePath(t *testing.T) {
	secret := "sk-supersecret123456"
	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"}, allRequiredValues(secret, "https://gateway.example.com", "claude-3-sonnet"), ""),
	})
	read.SourceIssues = []config.SourceIssue{
		{
			Kind:    config.SourceIssueMalformed,
			Status:  core.StatusFAIL,
			Source:  core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"},
			Summary: "malformed setting ANTHROPIC_AUTH_TOKEN=" + secret,
		},
	}

	result, err := Run(context.Background(), testSystem(nil), read, Options{NetworkChecks: false})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	report := Render(result, RenderOptions{})
	if strings.Contains(report, secret) {
		t.Fatalf("report leaked full secret:\n%s", report)
	}
	if !strings.Contains(report, "sk-...3456") {
		t.Fatalf("report missing masked secret:\n%s", report)
	}
	if !strings.Contains(report, "~/.claude/settings.json") {
		t.Fatalf("report missing shortened home path:\n%s", report)
	}
	if !strings.HasPrefix(report, "llmgate diagnosis: FAIL\n\n*Claude Code CLI*") {
		t.Fatalf("report header format changed:\n%s", report)
	}
}

func TestRenderKeepsFullReportCheckBasedWhenFindingsExist(t *testing.T) {
	result := Result{
		Sections: []core.DiagnosticSection{{
			ID:    "gateway",
			Title: "Gateway",
			Checks: []core.DiagnosticCheck{{
				ID:      "gateway.validation",
				Title:   "Gateway validation",
				Status:  core.StatusFAIL,
				Summary: "gateway validation failed",
				Details: []string{"reason: full gateway evidence"},
			}},
		}},
		Findings: []core.DiagnosticFinding{{
			ID:          "gateway.current",
			Status:      core.StatusFAIL,
			Title:       "Gateway: token rejected",
			Summary:     "The short finding summary should stay out of Review details for now.",
			Evidence:    []string{"HTTP status: 401"},
			Remediation: "Update the active token.",
		}},
	}

	report := Render(result, RenderOptions{})
	if !strings.Contains(report, "gateway validation failed") || !strings.Contains(report, "reason: full gateway evidence") {
		t.Fatalf("report lost check details:\n%s", report)
	}
	if strings.Contains(report, "Gateway: token rejected") || strings.Contains(report, "The short finding summary") {
		t.Fatalf("report rendered findings before full-report support was added:\n%s", report)
	}
}

func TestRenderKeepsSanitizedGatewayDetailInFullReport(t *testing.T) {
	token := "sk-reportsecret1234567890"
	longDetail := "gateway rejected token " + token + " from /home/ada/.cache/llmgate " +
		strings.Repeat("diagnostic context ", 20) + "LiteLLM_VerificationTokenTable"
	gatewayErr := &gateway.Error{
		Kind:       gateway.FailureAuth,
		Operation:  "model list",
		StatusCode: 401,
		URL:        "https://gateway.example.com/v1/models",
		Detail:     longDetail,
	}
	result := Result{
		Read: configReadForReport(token),
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
	}

	report := Render(result, RenderOptions{})

	if !strings.Contains(report, "LiteLLM_VerificationTokenTable") {
		t.Fatalf("report omitted full gateway detail:\n%s", report)
	}
	if strings.Contains(report, token) || strings.Contains(report, "/home/ada") {
		t.Fatalf("report leaked unsanitized detail:\n%s", report)
	}
	if !strings.Contains(report, "sk-...7890") || !strings.Contains(report, "~/.cache/llmgate") {
		t.Fatalf("report missing sanitized gateway detail:\n%s", report)
	}
}

func configReadForReport(token string) config.ReadResult {
	label := core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"}
	return config.ReadResult{
		Paths: configTestPaths(),
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

func configTestPaths() system.DiscoveredPaths {
	return system.DiscoveredPaths{HomeDir: "/home/ada", GOOS: "linux"}
}
