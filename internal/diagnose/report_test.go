package diagnose

import (
	"context"
	"strings"
	"testing"

	"github.com/r13v/llmgate/internal/config"
	"github.com/r13v/llmgate/internal/core"
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
