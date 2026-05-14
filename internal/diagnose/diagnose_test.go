package diagnose

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/r13v/llmgate/internal/config"
	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/gateway"
	"github.com/r13v/llmgate/internal/shell"
	"github.com/r13v/llmgate/internal/system"
)

func TestRunAggregatesOKAndSKIP(t *testing.T) {
	server := newGatewayServer(t, []string{"claude-3-haiku", "claude-3-sonnet", "claude-3-opus"})
	defer server.Close()

	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"}, allRequiredValues("sk-goodtoken1234", server.URL, "claude-3-sonnet"), ""),
	})
	sys := testSystem(nil)

	okResult, err := Run(context.Background(), sys, read, Options{
		NetworkChecks: true,
		Gateway:       testGateway(server),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := okResult.Status(); got != core.StatusOK {
		t.Fatalf("network-enabled status = %s, want OK\n%s", got, Render(okResult, RenderOptions{}))
	}
	if len(okResult.Findings) != 0 {
		t.Fatalf("network-enabled findings = %#v, want none", okResult.Findings)
	}
	if !sectionStatus(okResult, "Gateway", core.StatusOK) {
		t.Fatalf("Gateway section was not OK:\n%s", Render(okResult, RenderOptions{}))
	}

	skipResult, err := Run(context.Background(), sys, read, Options{NetworkChecks: false})
	if err != nil {
		t.Fatalf("Run() with network disabled error = %v", err)
	}
	if got := skipResult.Status(); got != core.StatusSKIP {
		t.Fatalf("network-disabled status = %s, want SKIP\n%s", got, Render(skipResult, RenderOptions{}))
	}
	if len(skipResult.Findings) != 0 {
		t.Fatalf("network-disabled findings = %#v, want none", skipResult.Findings)
	}
	if !sectionStatus(skipResult, "Gateway", core.StatusSKIP) {
		t.Fatalf("Gateway section was not SKIP:\n%s", Render(skipResult, RenderOptions{}))
	}
}

func TestRunDowngradesPersistedModelFailureAndDetectsRepairableStaleShellModel(t *testing.T) {
	models := []string{"claude-haiku-current", "claude-opus-current", "claude-sonnet-current"}
	server := newGatewayServer(t, models)
	defer server.Close()

	currentValues := allRequiredValues("sk-goodtoken1234", server.URL, "claude-sonnet-current")
	currentValues[core.VarAnthropicDefaultHaikuModel] = "claude-haiku-current"
	currentValues[core.VarAnthropicDefaultSonnetModel] = "claude-sonnet-current"
	currentValues[core.VarAnthropicDefaultOpusModel] = "claude-opus-current"

	persistedValues := allRequiredValues("sk-goodtoken1234", server.URL, "stale-shell-model")
	persistedValues[core.VarAnthropicDefaultHaikuModel] = "claude-haiku-current"
	persistedValues[core.VarAnthropicDefaultSonnetModel] = "claude-sonnet-current"
	persistedValues[core.VarAnthropicDefaultOpusModel] = "claude-opus-current"

	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceShellProfile, Path: "/home/ada/.zshrc"}, persistedValues, ""),
		testSource(core.SourceLabel{Kind: core.SourceCurrentEnv}, currentValues, ""),
	})

	result, err := Run(context.Background(), testSystem(nil), read, Options{
		NetworkChecks: true,
		Gateway:       testGateway(server),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := result.Status(); got != core.StatusWARN {
		t.Fatalf("status = %s, want WARN\n%s", got, Render(result, RenderOptions{}))
	}
	if len(result.RepairableStaleShellModelWarnings) != 1 {
		t.Fatalf("repairable warnings = %#v, want one", result.RepairableStaleShellModelWarnings)
	}
	warning := result.RepairableStaleShellModelWarnings[0]
	if warning.Name != core.VarAnthropicModel || warning.StaleValue.Value != "stale-shell-model" {
		t.Fatalf("repairable warning = %#v, want stale ANTHROPIC_MODEL", warning)
	}
	if !checkSummaryStatus(result, "Models (persisted config for new sessions)", "ANTHROPIC_MODEL model is unavailable", core.StatusWARN) {
		t.Fatalf("persisted stale model was not downgraded to WARN:\n%s", Render(result, RenderOptions{}))
	}
}

func TestRunFailsWhenNoUsableGatewayContext(t *testing.T) {
	server := newGatewayServer(t, []string{"claude-3-sonnet"})
	defer server.Close()

	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceCurrentEnv}, allRequiredValues("sk-badtoken1234", server.URL, "claude-3-sonnet"), ""),
	})

	result, err := Run(context.Background(), testSystem(nil), read, Options{
		NetworkChecks: true,
		Gateway:       testGateway(server),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := result.Status(); got != core.StatusFAIL {
		t.Fatalf("status = %s, want FAIL\n%s", got, Render(result, RenderOptions{}))
	}
	if !checkSummaryStatus(result, "Gateway (current environment)", "gateway validation failed", core.StatusFAIL) {
		t.Fatalf("current gateway failure was not FAIL:\n%s", Render(result, RenderOptions{}))
	}
	rendered := Render(result, RenderOptions{})
	for _, want := range []string{
		"reason: model list failed: auth HTTP 401",
		"request URL: " + server.URL + "/v1/models",
		"failure kind: auth",
		"HTTP status: 401",
		"what it means: The gateway rejected the configured ANTHROPIC_AUTH_TOKEN.",
		"fix: Update ANTHROPIC_AUTH_TOKEN",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("gateway failure details missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "sk-badtoken1234") {
		t.Fatalf("gateway failure details leaked token:\n%s", rendered)
	}

	finding := requireFinding(t, result, "gateway.current")
	if finding.Status != core.StatusFAIL || finding.Title != "Gateway: token rejected" {
		t.Fatalf("gateway finding = %#v, want FAIL token rejected", finding)
	}
	assertContains(t, finding.RelatedChecks, "gateway.validation.current-environment")
	assertContains(t, finding.Evidence, "request URL: "+server.URL+"/v1/models")
	assertContains(t, finding.Evidence, "failure kind: auth")
	assertContains(t, finding.Evidence, "HTTP status: 401")
	if strings.Contains(strings.Join(finding.Evidence, "\n"), "sk-badtoken1234") {
		t.Fatalf("gateway finding leaked token: %#v", finding.Evidence)
	}
}

func TestRunNewSessionModeRendersNewSessionContextAndIgnoresStaleProcessEnv(t *testing.T) {
	server := newGatewayServer(t, []string{"claude-3-sonnet"})
	defer server.Close()

	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceShellProfile, Path: "/home/ada/.zshrc"}, allRequiredValues("sk-goodtoken1234", server.URL, "claude-3-sonnet"), ""),
		testSource(core.SourceLabel{Kind: core.SourceCurrentEnv}, allRequiredValues("sk-staleprocess1234", "https://stale.example.com", "claude-stale"), ""),
	})

	result, err := Run(context.Background(), testSystem(nil), read, Options{
		NetworkChecks: true,
		Gateway:       testGateway(server),
		CurrentMode:   config.CurrentModeNewSession,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := result.Status(); got != core.StatusOK {
		t.Fatalf("status = %s, want OK\n%s", got, Render(result, RenderOptions{}))
	}
	if result.Resolution.Current.Name != "new terminal session" {
		t.Fatalf("Current.Name = %q, want new terminal session", result.Resolution.Current.Name)
	}
	if sectionStatus(result, "Runtime Environment", core.StatusOK) {
		t.Fatalf("new-session diagnostics should not render a runtime context comparison:\n%s", Render(result, RenderOptions{}))
	}
	for _, title := range []string{
		"Gateway (new terminal session)",
		"Models (new terminal session)",
		"Model Probes (new terminal session)",
	} {
		if !sectionStatus(result, title, core.StatusOK) {
			t.Fatalf("%s section was not OK:\n%s", title, Render(result, RenderOptions{}))
		}
	}

	rendered := Render(result, RenderOptions{})
	if !strings.Contains(rendered, "new terminal session") {
		t.Fatalf("rendered diagnostics missing new-session wording:\n%s", rendered)
	}
	if strings.Contains(rendered, "current environment") {
		t.Fatalf("new-session diagnostics should not mention current environment:\n%s", rendered)
	}
}

func TestRunNewSessionGatewayFindingUsesNewSessionContext(t *testing.T) {
	server := newGatewayServer(t, []string{"claude-3-sonnet"})
	defer server.Close()

	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceShellProfile, Path: "/home/ada/.zshrc"}, allRequiredValues("sk-badtoken1234", server.URL, "claude-3-sonnet"), ""),
		testSource(core.SourceLabel{Kind: core.SourceCurrentEnv}, allRequiredValues("sk-goodtoken1234", server.URL, "claude-3-sonnet"), ""),
	})

	result, err := Run(context.Background(), testSystem(nil), read, Options{
		NetworkChecks: true,
		Gateway:       testGateway(server),
		CurrentMode:   config.CurrentModeNewSession,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !checkSummaryStatus(result, "Gateway (new terminal session)", "gateway validation failed", core.StatusFAIL) {
		t.Fatalf("new-session gateway failure was not attributed to new terminal session:\n%s", Render(result, RenderOptions{}))
	}
	finding := requireFinding(t, result, "gateway.new-session")
	assertContains(t, finding.RelatedChecks, "gateway.validation.new-terminal-session")
	assertContains(t, finding.Evidence, "context: new terminal session")
}

func TestRunProbesAvailableModelsWhenAnotherSelectedModelIsUnavailable(t *testing.T) {
	server := newGatewayServer(t, []string{"claude-good", "claude-probe-fail"})
	defer server.Close()

	values := allRequiredValues("sk-goodtoken1234", server.URL, "claude-good")
	values[core.VarAnthropicDefaultHaikuModel] = "claude-missing"
	values[core.VarAnthropicDefaultSonnetModel] = "claude-probe-fail"
	values[core.VarAnthropicDefaultOpusModel] = "claude-good"
	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"}, values, ""),
	})

	result, err := Run(context.Background(), testSystem(nil), read, Options{
		NetworkChecks: true,
		Gateway:       testGateway(server),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !checkSummaryStatus(result, "Model Probes", `probe failed for model "claude-probe-fail"`, core.StatusFAIL) {
		t.Fatalf("available model probe failure was not reported:\n%s", Render(result, RenderOptions{}))
	}
	finding := requireFinding(t, result, "gateway.probe.current.claude-probe-fail")
	if finding.Title != "Gateway: model probe failed" {
		t.Fatalf("probe finding title = %q, want Gateway: model probe failed", finding.Title)
	}
	assertContains(t, finding.RelatedChecks, "model-probes.claude-probe-fail")
	assertContains(t, finding.Evidence, `subject: model "claude-probe-fail"`)
	assertContains(t, finding.Evidence, "HTTP status: 400")
}

func TestRunBypassesCachedGatewayFailuresWhenRequested(t *testing.T) {
	t.Run("model list", func(t *testing.T) {
		var failModels atomic.Bool
		var modelRequests atomic.Int64
		failModels.Store(true)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/models":
				modelRequests.Add(1)
				if failModels.Load() {
					w.WriteHeader(http.StatusBadGateway)
					_, _ = w.Write([]byte(`{"detail":"upstream unavailable"}`))
					return
				}
				writeJSON(t, w, map[string]any{"data": []map[string]string{{"id": "claude-3-sonnet"}}})
			case "/v1/chat/completions":
				writeJSON(t, w, map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": ""}}}})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		client := testGateway(server)
		read := testRead([]config.Source{
			testSource(core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"}, allRequiredValues("sk-goodtoken1234", server.URL, "claude-3-sonnet"), ""),
		})

		first, err := Run(context.Background(), testSystem(nil), read, Options{
			NetworkChecks: true,
			Gateway:       client,
		})
		if err != nil {
			t.Fatalf("first Run() error = %v", err)
		}
		if got := first.Status(); got != core.StatusFAIL {
			t.Fatalf("first status = %s, want FAIL\n%s", got, Render(first, RenderOptions{}))
		}

		failModels.Store(false)
		second, err := Run(context.Background(), testSystem(nil), read, Options{
			NetworkChecks:            true,
			Gateway:                  client,
			BypassFailedGatewayCache: true,
		})
		if err != nil {
			t.Fatalf("second Run() error = %v", err)
		}
		if got := second.Status(); got != core.StatusOK {
			t.Fatalf("second status = %s, want OK\n%s", got, Render(second, RenderOptions{}))
		}
		if got := modelRequests.Load(); got != 2 {
			t.Fatalf("model list requests = %d, want 2 to prove failed cache bypass", got)
		}
	})

	t.Run("model probe", func(t *testing.T) {
		var failProbe atomic.Bool
		var probeRequests atomic.Int64
		failProbe.Store(true)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/models":
				writeJSON(t, w, map[string]any{"data": []map[string]string{{"id": "claude-3-sonnet"}}})
			case "/v1/chat/completions":
				probeRequests.Add(1)
				if failProbe.Load() {
					w.WriteHeader(http.StatusBadGateway)
					_, _ = w.Write([]byte(`{"detail":"probe failed"}`))
					return
				}
				writeJSON(t, w, map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": ""}}}})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		client := testGateway(server)
		read := testRead([]config.Source{
			testSource(core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"}, allRequiredValues("sk-goodtoken1234", server.URL, "claude-3-sonnet"), ""),
		})

		first, err := Run(context.Background(), testSystem(nil), read, Options{
			NetworkChecks: true,
			Gateway:       client,
		})
		if err != nil {
			t.Fatalf("first Run() error = %v", err)
		}
		if got := first.Status(); got != core.StatusFAIL {
			t.Fatalf("first status = %s, want FAIL\n%s", got, Render(first, RenderOptions{}))
		}

		failProbe.Store(false)
		second, err := Run(context.Background(), testSystem(nil), read, Options{
			NetworkChecks:            true,
			Gateway:                  client,
			BypassFailedGatewayCache: true,
		})
		if err != nil {
			t.Fatalf("second Run() error = %v", err)
		}
		if got := second.Status(); got != core.StatusOK {
			t.Fatalf("second status = %s, want OK\n%s", got, Render(second, RenderOptions{}))
		}
		if got := probeRequests.Load(); got != 2 {
			t.Fatalf("probe requests = %d, want 2 to prove failed cache bypass", got)
		}
	})
}

func TestRunReportsConfigSourceConflictsWithRedaction(t *testing.T) {
	claudeValues := allRequiredValues("sk-claudeconflict1234", "https://gateway.example.com", "claude-3-sonnet")
	shellValues := allRequiredValues("sk-shellconflict5678", "https://gateway.example.com", "claude-3-sonnet")
	shellSource := testSource(core.SourceLabel{Kind: core.SourceShellProfile, Path: "/home/ada/.zshrc"}, shellValues, "")
	shellSource.ShellIssues = []shell.Issue{
		{
			Kind:    shell.IssueDuplicate,
			Name:    core.VarAnthropicModel,
			Lines:   []int{4, 8},
			Summary: core.VarAnthropicModel + " has multiple active simple shell assignments on lines 4, 8",
		},
		{
			Kind:    shell.IssueDynamic,
			Name:    core.VarAnthropicDefaultHaikuModel,
			Line:    12,
			Summary: core.VarAnthropicDefaultHaikuModel + " uses a dynamic shell assignment on line 12",
		},
	}
	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"}, claudeValues, ""),
		shellSource,
	})

	result, err := Run(context.Background(), testSystem(nil), read, Options{NetworkChecks: false})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	rendered := Render(result, RenderOptions{})
	if !sectionStatus(result, "Config Source Conflicts", core.StatusWARN) {
		t.Fatalf("conflict section was not WARN:\n%s", rendered)
	}
	for _, want := range []string{
		core.VarAnthropicAuthToken + " differs across persisted sources",
		core.VarAnthropicModel + " has multiple active simple shell assignments on lines 4, 8",
		core.VarAnthropicDefaultHaikuModel + " uses a dynamic shell assignment on line 12",
		"sk-[redacted]",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("conflict report missing %q:\n%s", want, rendered)
		}
	}
	for _, secret := range []string{"sk-claudeconflict1234", "sk-shellconflict5678"} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("conflict report leaked secret %q:\n%s", secret, rendered)
		}
	}

	finding := requireFinding(t, result, "config-conflict.anthropic-auth-token")
	if finding.Status != core.StatusWARN || finding.Title != "Config: ANTHROPIC_AUTH_TOKEN differs across sources" {
		t.Fatalf("token conflict finding = %#v, want grouped WARN", finding)
	}
	if !strings.Contains(finding.Summary, "2 distinct values") {
		t.Fatalf("token conflict summary = %q, want distinct value count", finding.Summary)
	}
	assertContains(t, finding.RelatedChecks, "config-conflict.01.ANTHROPIC_AUTH_TOKEN")
	assertContains(t, finding.Evidence, "distinct values: 2")
	if strings.Contains(strings.Join(finding.Evidence, "\n"), "sk-claudeconflict1234") ||
		strings.Contains(strings.Join(finding.Evidence, "\n"), "sk-shellconflict5678") {
		t.Fatalf("token conflict finding leaked secret values: %#v", finding.Evidence)
	}
}

func TestProjectAndIDEValidationWarnSeparately(t *testing.T) {
	server := newGatewayServer(t, []string{"claude-3-haiku", "claude-3-sonnet", "claude-3-opus"})
	defer server.Close()

	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"}, allRequiredValues("sk-goodtoken1234", server.URL, "claude-3-sonnet"), ""),
		testSource(core.SourceLabel{Kind: core.SourceProjectLocalSettings, Path: "/home/ada/project/.claude/settings.local.json"}, allRequiredValues("sk-badtoken1234", server.URL, "claude-3-sonnet"), ""),
		testSource(core.SourceLabel{Kind: core.SourceVSCodeSettings, Path: "/home/ada/.config/Code/User/settings.json"}, nil, "unavailable-ide-model"),
	})

	result, err := Run(context.Background(), testSystem(nil), read, Options{
		NetworkChecks: true,
		Gateway:       testGateway(server),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !sectionStatus(result, "Project Config Validation", core.StatusWARN) {
		t.Fatalf("project validation was not WARN:\n%s", Render(result, RenderOptions{}))
	}
	if !sectionStatus(result, "IDE Config Validation", core.StatusWARN) {
		t.Fatalf("IDE validation was not WARN:\n%s", Render(result, RenderOptions{}))
	}
	if !checkSummaryStatus(result, "IDE Config Validation", "ANTHROPIC_MODEL model is unavailable", core.StatusWARN) {
		t.Fatalf("IDE unavailable selected model warning missing:\n%s", Render(result, RenderOptions{}))
	}
}

func TestRunBypassesCachedSideContextFailuresWhenRequested(t *testing.T) {
	projectToken := "sk-projectgood1234"
	ideToken := "sk-idegood1234"
	var failProject atomic.Bool
	var failIDE atomic.Bool
	var projectRequests atomic.Int64
	var ideRequests atomic.Int64
	failProject.Store(true)
	failIDE.Store(true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			switch token {
			case projectToken:
				projectRequests.Add(1)
				if failProject.Load() {
					w.WriteHeader(http.StatusBadGateway)
					_, _ = w.Write([]byte(`{"detail":"project gateway stale failure"}`))
					return
				}
			case ideToken:
				ideRequests.Add(1)
				if failIDE.Load() {
					w.WriteHeader(http.StatusBadGateway)
					_, _ = w.Write([]byte(`{"detail":"IDE gateway stale failure"}`))
					return
				}
			}
			writeJSON(t, w, map[string]any{"data": []map[string]string{{"id": "claude-3-sonnet"}}})
		case "/v1/chat/completions":
			writeJSON(t, w, map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": ""}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := testGateway(server)
	if _, err := client.ListModels(context.Background(), server.URL, projectToken, gateway.RequestOptions{}); err == nil {
		t.Fatal("project side pre-cache ListModels() error = nil, want failure")
	}
	if _, err := client.ListModels(context.Background(), server.URL, ideToken, gateway.RequestOptions{}); err == nil {
		t.Fatal("IDE side pre-cache ListModels() error = nil, want failure")
	}
	failProject.Store(false)
	failIDE.Store(false)

	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"}, allRequiredValues("sk-goodtoken1234", server.URL, "claude-3-sonnet"), ""),
		testSource(core.SourceLabel{Kind: core.SourceProjectLocalSettings, Path: "/home/ada/project/.claude/settings.local.json"}, allRequiredValues(projectToken, server.URL, "claude-3-sonnet"), ""),
		testSource(core.SourceLabel{Kind: core.SourceVSCodeSettings, Path: "/home/ada/.config/Code/User/settings.json"}, allRequiredValues(ideToken, server.URL, "claude-3-sonnet"), ""),
	})

	result, err := Run(context.Background(), testSystem(nil), read, Options{
		NetworkChecks:            true,
		Gateway:                  client,
		CurrentMode:              config.CurrentModeNewSession,
		BypassFailedGatewayCache: true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !sectionStatus(result, "Project Config Validation", core.StatusOK) {
		t.Fatalf("project validation did not bypass cached failure:\n%s", Render(result, RenderOptions{}))
	}
	if !sectionStatus(result, "IDE Config Validation", core.StatusOK) {
		t.Fatalf("IDE validation did not bypass cached failure:\n%s", Render(result, RenderOptions{}))
	}
	if got := projectRequests.Load(); got != 2 {
		t.Fatalf("project model list requests = %d, want 2 to prove failed cache bypass", got)
	}
	if got := ideRequests.Load(); got != 2 {
		t.Fatalf("IDE model list requests = %d, want 2 to prove failed cache bypass", got)
	}
}

func TestRunBuildsSideContextGatewayFindings(t *testing.T) {
	server := newGatewayServer(t, []string{"claude-3-haiku", "claude-3-sonnet", "claude-3-opus"})
	defer server.Close()

	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"}, allRequiredValues("sk-goodtoken1234", server.URL, "claude-3-sonnet"), ""),
		testSource(core.SourceLabel{Kind: core.SourceProjectLocalSettings, Path: "/home/ada/project/.claude/settings.local.json"}, allRequiredValues("sk-projectbad1234", server.URL, "claude-3-sonnet"), ""),
		testSource(core.SourceLabel{Kind: core.SourceCursorSettings, Path: "/home/ada/.config/Cursor/User/settings.json"}, allRequiredValues("sk-cursorbad1234", server.URL, "claude-3-sonnet"), ""),
		testSource(core.SourceLabel{Kind: core.SourceVSCodeSettings, Path: "/home/ada/.config/Code/User/settings.json"}, allRequiredValues("sk-vscodebad1234", server.URL, "claude-3-sonnet"), ""),
	})

	result, err := Run(context.Background(), testSystem(nil), read, Options{
		NetworkChecks: true,
		Gateway:       testGateway(server),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	for _, tc := range []struct {
		id      string
		title   string
		checkID string
	}{
		{"gateway.side.project-local-settings", "Project gateway: token rejected", "project.project_local_settings.gateway"},
		{"gateway.side.cursor-settings", "Cursor gateway: token rejected", "IDE.cursor_settings.gateway"},
		{"gateway.side.vscode-settings", "VS Code gateway: token rejected", "IDE.vscode_settings.gateway"},
	} {
		finding := requireFinding(t, result, tc.id)
		if finding.Status != core.StatusWARN || finding.Title != tc.title {
			t.Fatalf("%s finding = %#v, want WARN %q", tc.id, finding, tc.title)
		}
		assertContains(t, finding.RelatedChecks, tc.checkID)
		assertContains(t, finding.Evidence, "failure kind: auth")
		assertContains(t, finding.Evidence, "HTTP status: 401")
	}
}

func TestValidateSideContextRetainsNonAuthGatewayFailureKinds(t *testing.T) {
	tests := []struct {
		name         string
		label        string
		source       core.SourceLabel
		baseURL      string
		status       int
		body         string
		wantTitle    string
		wantCheckID  string
		wantEvidence []string
	}{
		{
			name:        "project invalid URL",
			label:       "Project",
			source:      core.SourceLabel{Kind: core.SourceProjectLocalSettings},
			baseURL:     "not-a-url",
			wantTitle:   "Project gateway: invalid base URL",
			wantCheckID: "Project.project_local_settings.gateway",
			wantEvidence: []string{
				"failure kind: invalid-url",
			},
		},
		{
			name:        "cursor HTTP failure",
			label:       "IDE",
			source:      core.SourceLabel{Kind: core.SourceCursorSettings},
			status:      http.StatusBadGateway,
			body:        `{"detail":"upstream failed"}`,
			wantTitle:   "Cursor gateway: HTTP request failed",
			wantCheckID: "IDE.cursor_settings.gateway",
			wantEvidence: []string{
				"failure kind: http",
				"HTTP status: 502",
			},
		},
		{
			name:        "VS Code invalid JSON",
			label:       "IDE",
			source:      core.SourceLabel{Kind: core.SourceVSCodeSettings},
			status:      http.StatusOK,
			body:        `{not-json`,
			wantTitle:   "VS Code gateway: invalid response",
			wantCheckID: "IDE.vscode_settings.gateway",
			wantEvidence: []string{
				"failure kind: invalid-json",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseURL := tt.baseURL
			var client gateway.Client
			if tt.status != 0 || tt.body != "" {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte(tt.body))
				}))
				defer server.Close()
				baseURL = server.URL
				client = testGateway(server)
			}

			side := sideValidationContext{
				name:    tt.source.String(),
				source:  tt.source,
				token:   "sk-goodtoken1234",
				baseURL: baseURL,
			}
			validation := validateSideContext(context.Background(), side, Options{
				NetworkChecks: true,
				Gateway:       client,
			}, tt.label)
			if validation.GatewayFailure == nil {
				t.Fatalf("GatewayFailure missing: %#v", validation)
			}

			finding := gatewayErrorFinding(gatewayFindingInput{
				ID:            "test",
				Prefix:        sideGatewayPrefix(validation.GatewayFailure.Source),
				Scope:         validation.GatewayFailure.Name,
				Err:           validation.GatewayFailure.Err,
				Status:        core.StatusWARN,
				RelatedChecks: []string{validation.GatewayFailure.CheckID},
			})
			if finding.Title != tt.wantTitle {
				t.Fatalf("Title = %q, want %q", finding.Title, tt.wantTitle)
			}
			assertContains(t, finding.RelatedChecks, tt.wantCheckID)
			for _, want := range tt.wantEvidence {
				assertContains(t, finding.Evidence, want)
			}
		})
	}
}

func TestRunBuildsGroupedIDEDriftFinding(t *testing.T) {
	currentValues := map[string]string{core.VarAnthropicAuthToken: "sk-terminaltoken1234"}
	cursorValues := map[string]string{core.VarAnthropicAuthToken: "sk-cursortoken1234"}
	vscodeValues := map[string]string{core.VarAnthropicAuthToken: "sk-vscodetoken1234"}
	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceCurrentEnv}, currentValues, ""),
		testSource(core.SourceLabel{Kind: core.SourceCursorSettings, Path: "/home/ada/.config/Cursor/User/settings.json"}, cursorValues, ""),
		testSource(core.SourceLabel{Kind: core.SourceVSCodeSettings, Path: "/home/ada/.config/Code/User/settings.json"}, vscodeValues, ""),
	})

	result, err := Run(context.Background(), testSystem(nil), read, Options{NetworkChecks: false})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	finding := requireFinding(t, result, "ide-drift.anthropic-auth-token")
	if finding.Status != core.StatusWARN || finding.Title != "IDE: ANTHROPIC_AUTH_TOKEN differs from current environment" {
		t.Fatalf("IDE drift finding = %#v, want grouped WARN", finding)
	}
	if !strings.Contains(finding.Summary, "2 IDE sources") {
		t.Fatalf("IDE drift summary = %q, want grouped IDE sources", finding.Summary)
	}
	assertContains(t, finding.RelatedChecks, "ide-config.01.ANTHROPIC_AUTH_TOKEN")
	assertContains(t, finding.RelatedChecks, "ide-config.02.ANTHROPIC_AUTH_TOKEN")
	assertContains(t, finding.Evidence, "distinct IDE values: 2")
	assertContains(t, finding.Evidence, "compared against: current environment")
}

func TestRunNewSessionIDEDriftComparesAgainstNewTerminalSession(t *testing.T) {
	persistedValues := allRequiredValues("sk-goodtoken1234", "https://gateway.example.com", "claude-new-session")
	currentValues := allRequiredValues("sk-goodtoken1234", "https://gateway.example.com", "claude-stale-process")
	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceShellProfile, Path: "/home/ada/.zshrc"}, persistedValues, ""),
		testSource(core.SourceLabel{Kind: core.SourceCurrentEnv}, currentValues, ""),
		testSource(core.SourceLabel{Kind: core.SourceVSCodeSettings, Path: "/home/ada/.config/Code/User/settings.json"}, nil, "claude-new-session"),
	})

	processResult, err := Run(context.Background(), testSystem(nil), read, Options{NetworkChecks: false})
	if err != nil {
		t.Fatalf("process Run() error = %v", err)
	}
	if len(processResult.Resolution.IDEDrift) == 0 {
		t.Fatalf("process-mode IDE drift missing, test no longer proves new-session comparison")
	}

	newSessionResult, err := Run(context.Background(), testSystem(nil), read, Options{
		NetworkChecks: false,
		CurrentMode:   config.CurrentModeNewSession,
	})
	if err != nil {
		t.Fatalf("new-session Run() error = %v", err)
	}
	if len(newSessionResult.Resolution.IDEDrift) != 0 {
		t.Fatalf("new-session IDE drift = %#v, want none", newSessionResult.Resolution.IDEDrift)
	}
	if !sectionStatus(newSessionResult, "IDE Config", core.StatusOK) {
		t.Fatalf("IDE Config section was not OK:\n%s", Render(newSessionResult, RenderOptions{}))
	}
	rendered := Render(newSessionResult, RenderOptions{})
	if !strings.Contains(rendered, "IDE Claude settings match new terminal session") {
		t.Fatalf("IDE section did not use new-session comparison label:\n%s", rendered)
	}
}

func TestRunConnectsGatewayAuthFindingToTokenConflict(t *testing.T) {
	server := newGatewayServer(t, []string{"claude-3-sonnet"})
	defer server.Close()

	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"}, allRequiredValues("sk-claudebad1234", server.URL, "claude-3-sonnet"), ""),
		testSource(core.SourceLabel{Kind: core.SourceShellProfile, Path: "/home/ada/.zshrc"}, allRequiredValues("sk-shellbad5678", server.URL, "claude-3-sonnet"), ""),
	})

	result, err := Run(context.Background(), testSystem(nil), read, Options{
		NetworkChecks: true,
		Gateway:       testGateway(server),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	gatewayFinding := requireFinding(t, result, "gateway.current")
	if !strings.Contains(gatewayFinding.Summary, "configured token values differ") {
		t.Fatalf("gateway finding summary = %q, want token conflict connection", gatewayFinding.Summary)
	}
	assertContains(t, gatewayFinding.RelatedChecks, "gateway.validation.current-environment")
	assertContains(t, gatewayFinding.RelatedChecks, "config-conflict.01.ANTHROPIC_AUTH_TOKEN")
	assertContains(t, gatewayFinding.Evidence, "related config: ANTHROPIC_AUTH_TOKEN differs across Claude Code user settings (/home/ada/.claude/settings.json), terminal shell profile (/home/ada/.zshrc)")

	conflictFinding := requireFinding(t, result, "config-conflict.anthropic-auth-token")
	assertContains(t, conflictFinding.RelatedChecks, "config-conflict.01.ANTHROPIC_AUTH_TOKEN")
}

func TestRunConnectsGatewayAuthFindingToIDEDrift(t *testing.T) {
	server := newGatewayServer(t, []string{"claude-3-sonnet"})
	defer server.Close()

	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceCurrentEnv}, allRequiredValues("sk-terminalbad1234", server.URL, "claude-3-sonnet"), ""),
		testSource(core.SourceLabel{Kind: core.SourceCursorSettings, Path: "/home/ada/.config/Cursor/User/settings.json"}, map[string]string{core.VarAnthropicAuthToken: "sk-cursorbad1234"}, ""),
		testSource(core.SourceLabel{Kind: core.SourceVSCodeSettings, Path: "/home/ada/.config/Code/User/settings.json"}, map[string]string{core.VarAnthropicAuthToken: "sk-vscodebad1234"}, ""),
	})

	result, err := Run(context.Background(), testSystem(nil), read, Options{
		NetworkChecks: true,
		Gateway:       testGateway(server),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	gatewayFinding := requireFinding(t, result, "gateway.current")
	if !strings.Contains(gatewayFinding.Summary, "configured token values differ") {
		t.Fatalf("gateway finding summary = %q, want IDE drift connection", gatewayFinding.Summary)
	}
	assertContains(t, gatewayFinding.RelatedChecks, "gateway.validation.current-environment")
	assertContains(t, gatewayFinding.RelatedChecks, "ide-config.01.ANTHROPIC_AUTH_TOKEN")
	assertContains(t, gatewayFinding.RelatedChecks, "ide-config.02.ANTHROPIC_AUTH_TOKEN")
	for _, checkID := range gatewayFinding.RelatedChecks {
		if strings.HasPrefix(checkID, "config-conflict.") {
			t.Fatalf("gateway finding unexpectedly linked config conflict check: %#v", gatewayFinding.RelatedChecks)
		}
	}
	hasIDEEvidence := false
	for _, evidence := range gatewayFinding.Evidence {
		if strings.HasPrefix(evidence, "related IDE: ANTHROPIC_AUTH_TOKEN differs in ") {
			hasIDEEvidence = true
			break
		}
	}
	if !hasIDEEvidence {
		t.Fatalf("gateway finding missing related IDE evidence: %#v", gatewayFinding.Evidence)
	}
}

func TestRunReportsCommandFailureAsWarning(t *testing.T) {
	server := newGatewayServer(t, []string{"claude-3-sonnet"})
	defer server.Close()

	read := testRead([]config.Source{
		testSource(core.SourceLabel{Kind: core.SourceClaudeUserSettings, Path: "/home/ada/.claude/settings.json"}, allRequiredValues("sk-goodtoken1234", server.URL, "claude-3-sonnet"), ""),
	})
	sys := testSystem(errors.New("not found"))

	result, err := Run(context.Background(), sys, read, Options{
		NetworkChecks: true,
		Gateway:       testGateway(server),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !sectionStatus(result, "Claude Code CLI", core.StatusWARN) {
		t.Fatalf("CLI failure was not WARN:\n%s", Render(result, RenderOptions{}))
	}
}

func testRead(sources []config.Source) config.ReadResult {
	return config.ReadResult{
		Approved: true,
		Paths: system.DiscoveredPaths{
			GOOS:    "linux",
			HomeDir: "/home/ada",
		},
		Sources: sources,
		WriteTargets: []core.WriteTarget{
			{
				Kind:      core.WriteTargetClaudeUserSettings,
				Title:     core.WriteTargetTitle(core.WriteTargetClaudeUserSettings),
				Path:      "/home/ada/.claude/settings.json",
				Sensitive: true,
				Writable:  true,
				Exists:    true,
			},
			{
				Kind:      core.WriteTargetShellProfile,
				Title:     core.WriteTargetTitle(core.WriteTargetShellProfile),
				Path:      "/home/ada/.zshrc",
				Sensitive: true,
				Writable:  true,
				Exists:    true,
			},
		},
	}
}

func allRequiredValues(token, baseURL, model string) map[string]string {
	return map[string]string{
		core.VarAnthropicAuthToken:          token,
		core.VarAnthropicBaseURL:            baseURL,
		core.VarAnthropicModel:              model,
		core.VarAnthropicDefaultHaikuModel:  model,
		core.VarAnthropicDefaultSonnetModel: model,
		core.VarAnthropicDefaultOpusModel:   model,
	}
}

func testSource(label core.SourceLabel, values map[string]string, selectedModel string) config.Source {
	source := config.Source{
		Label:  label,
		Values: make(map[string]core.ConfigValue),
	}
	for name, value := range values {
		if !core.IsManaged(name) {
			continue
		}
		source.Values[name] = core.ConfigValue{
			Name:   name,
			Value:  value,
			Source: label,
			Secret: core.IsSecret(name),
		}
	}
	if selectedModel != "" {
		selectedLabel := label
		selectedLabel.Detail = "selected model"
		selected := core.ConfigValue{
			Name:   core.VarAnthropicModel,
			Value:  selectedModel,
			Source: selectedLabel,
		}
		source.SelectedModel = &selected
	}
	return source
}

func testGateway(server *httptest.Server) gateway.Client {
	return gateway.Client{
		HTTPClient: server.Client(),
		Cache:      gateway.NewCache(),
	}
}

func newGatewayServer(t *testing.T, models []string) *httptest.Server {
	t.Helper()

	modelSet := make(map[string]bool, len(models))
	for _, model := range models {
		modelSet[model] = true
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-goodtoken1234" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":"gateway rejected token sk-badtoken1234"}`))
			return
		}
		data := make([]map[string]string, 0, len(models))
		for _, model := range models {
			data = append(data, map[string]string{"id": model})
		}
		writeJSON(t, w, map[string]any{"data": data})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-goodtoken1234" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":"gateway rejected token sk-badtoken1234"}`))
			return
		}
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode probe payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
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

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func testSystem(commandErr error) system.System {
	runner := &testCommandRunner{
		result: system.CommandResult{Stdout: "claude 1.0.0\n"},
		err:    commandErr,
	}
	if commandErr != nil {
		runner.result = system.CommandResult{Stderr: "not found\n", ExitCode: 127}
	}
	return system.System{Commands: runner}
}

type testCommandRunner struct {
	result system.CommandResult
	err    error
}

func (r *testCommandRunner) Run(context.Context, string, ...string) (system.CommandResult, error) {
	return r.result, r.err
}

func sectionStatus(result Result, title string, status core.DiagnosticStatus) bool {
	for _, section := range result.Sections {
		if section.Title == title {
			return section.Status() == status
		}
	}
	return false
}

func checkSummaryStatus(result Result, sectionTitle, summary string, status core.DiagnosticStatus) bool {
	for _, section := range result.Sections {
		if section.Title != sectionTitle {
			continue
		}
		for _, check := range section.Checks {
			if check.Summary == summary && check.Status == status {
				return true
			}
		}
	}
	return false
}

func requireFinding(t *testing.T, result Result, id string) core.DiagnosticFinding {
	t.Helper()
	for _, finding := range result.Findings {
		if finding.ID == id {
			return finding
		}
	}
	t.Fatalf("finding %q not found in %#v", id, result.Findings)
	return core.DiagnosticFinding{}
}

func assertContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("%#v does not contain %q", values, want)
}
