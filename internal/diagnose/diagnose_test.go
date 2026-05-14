package diagnose

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
		"what it means: the gateway rejected the configured token",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("gateway failure details missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "sk-badtoken1234") {
		t.Fatalf("gateway failure details leaked token:\n%s", rendered)
	}
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
