//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/r13v/llmgate/internal/config"
	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/gateway"
	"github.com/r13v/llmgate/internal/wizard"
)

func TestAcceptanceCLIAndPrivacyScenarios(t *testing.T) {
	t.Run("startup decline has no side effects", func(t *testing.T) {
		h := newHarness(t)
		output, err := h.runAccessible("n\n")
		if !errors.Is(err, wizard.ErrStartupDeclined) {
			t.Fatalf("Run() error = %v, want ErrStartupDeclined\n%s", err, output)
		}
		assertContains(t, output, "No files were read or changed.")
		if h.fs.readOps != 0 || h.fs.statOps != 0 || h.fs.mutationOps() != 0 {
			t.Fatalf("startup decline touched filesystem: reads=%d stats=%d mutations=%d", h.fs.readOps, h.fs.statOps, h.fs.mutationOps())
		}
		if h.env.readOps() != 0 || h.commands.calls != 0 || h.gateway.listCalls != 0 || h.gateway.probeCalls != 0 {
			t.Fatalf("startup decline touched local or network state: env=%d commands=%d list=%d probe=%d", h.env.readOps(), h.commands.calls, h.gateway.listCalls, h.gateway.probeCalls)
		}
	})

	t.Run("non-interactive no-arg path fails clearly", func(t *testing.T) {
		h := newHarness(t)
		output, err := h.runScripted(nil, nonInteractive())
		if !errors.Is(err, wizard.ErrNonInteractive) {
			t.Fatalf("Run() error = %v, want ErrNonInteractive\n%s", err, output)
		}
	})

	t.Run("setup output redacts token and home path", func(t *testing.T) {
		h := newHarness(t)
		output, err := h.runScripted(freshSetupResponses(h.gateway.url(), nil))
		if err != nil {
			t.Fatalf("Run() error = %v\n%s", err, output)
		}
		assertNoSecretLeak(t, output, testToken)
		assertContains(t, output, "~/.claude/settings.json")
		assertNotContains(t, output, "/home/ada/.claude/settings.json")
	})

	t.Run("gateway error body token is redacted", func(t *testing.T) {
		h := newHarness(t, withGatewayOptions(t, fakeGatewayOptions{includeTokenBody: true}))
		prompts := &scriptedPrompter{responses: []promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "setup"},
			{kind: "input", value: leakedToken},
			{kind: "input", value: h.gateway.url()},
			{kind: "select", value: "exit"},
		}}
		output, err := h.runWithPrompter(prompts)
		if err != nil {
			t.Fatalf("Run() error = %v\n%s", err, output)
		}
		assertNoSecretLeak(t, output, leakedToken)
		for _, record := range prompts.records {
			assertNoSecretLeak(t, record.description, leakedToken)
		}
		if h.fs.mutationOps() != 0 {
			t.Fatalf("gateway failure wrote filesystem %d time(s)", h.fs.mutationOps())
		}
	})
}

func TestAcceptanceGatewayAndModelSelectionScenarios(t *testing.T) {
	t.Run("model list normalization fallback sorting and failures", func(t *testing.T) {
		h := newHarness(t, withGatewayOptions(t, fakeGatewayOptions{
			models:       []string{sonnetModel, haikuModel, sonnetModel, opusModel},
			fallbackOnly: true,
		}))
		client := h.gateway.client()
		result, err := client.ListModels(context.Background(), h.gateway.url()+"/v1?ignored=true#fragment", testToken, gateway.RequestOptions{})
		if err != nil {
			t.Fatalf("ListModels() error = %v", err)
		}
		if !result.FallbackUsed || h.gateway.fallbackCalls != 1 {
			t.Fatalf("fallback not used: result=%+v fallbackCalls=%d", result, h.gateway.fallbackCalls)
		}
		if got, want := strings.Join(result.Models, ","), strings.Join(sortedCopy(recommendedModels), ","); got != want {
			t.Fatalf("models = %q, want %q", got, want)
		}

		for _, tc := range []struct {
			name string
			resp gatewayResponse
			kind gateway.FailureKind
		}{
			{name: "auth", resp: gatewayResponse{status: http.StatusUnauthorized, body: `{"detail":"bad token"}`}, kind: gateway.FailureAuth},
			{name: "http", resp: gatewayResponse{status: http.StatusBadGateway, body: `{"detail":"upstream failed"}`}, kind: gateway.FailureHTTP},
			{name: "invalid-json", resp: gatewayResponse{status: http.StatusOK, body: `{not-json`}, kind: gateway.FailureInvalidJSON},
			{name: "empty", resp: gatewayResponse{status: http.StatusOK, body: `{"data":[]}`}, kind: gateway.FailureEmptyModels},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := newHarness(t, withGatewayOptions(t, fakeGatewayOptions{listResponses: []gatewayResponse{tc.resp}}))
				_, err := h.gateway.client().ListModels(context.Background(), h.gateway.url(), testToken, gateway.RequestOptions{})
				var gerr *gateway.Error
				if !errors.As(err, &gerr) || gerr.Kind != tc.kind {
					t.Fatalf("ListModels() error = %v, want kind %s", err, tc.kind)
				}
			})
		}
	})

	t.Run("gateway cache and setup retry behavior", func(t *testing.T) {
		h := newHarness(t)
		client := h.gateway.client()
		first, err := client.ListModels(context.Background(), h.gateway.url(), testToken, gateway.RequestOptions{})
		if err != nil {
			t.Fatalf("first ListModels() error = %v", err)
		}
		second, err := client.ListModels(context.Background(), h.gateway.url(), testToken, gateway.RequestOptions{})
		if err != nil {
			t.Fatalf("second ListModels() error = %v", err)
		}
		if first.Cached || !second.Cached || h.gateway.listCalls != 1 {
			t.Fatalf("cache result mismatch: first=%t second=%t listCalls=%d", first.Cached, second.Cached, h.gateway.listCalls)
		}
		if _, err := client.ProbeModel(context.Background(), h.gateway.url(), testToken, sonnetModel, gateway.RequestOptions{}); err != nil {
			t.Fatalf("first ProbeModel() error = %v", err)
		}
		probe, err := client.ProbeModel(context.Background(), h.gateway.url(), testToken, sonnetModel, gateway.RequestOptions{})
		if err != nil {
			t.Fatalf("second ProbeModel() error = %v", err)
		}
		if !probe.Cached || h.gateway.probeCalls != 1 || h.gateway.probePingBodies != 1 {
			t.Fatalf("probe cache/ping mismatch: cached=%t calls=%d pings=%d", probe.Cached, h.gateway.probeCalls, h.gateway.probePingBodies)
		}

		retry := newHarness(t, withGatewayOptions(t, fakeGatewayOptions{
			listResponses: []gatewayResponse{{status: http.StatusBadGateway, body: `{"detail":"temporary failure"}`}},
		}))
		output, err := retry.runScripted([]promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "setup"},
			{kind: "input", value: testToken},
			{kind: "input", value: retry.gateway.url()},
			{kind: "select", value: "retry"},
			{kind: "confirm", confirm: true},
			{kind: "multiselect"},
			{kind: "confirm", confirm: true},
		})
		if err != nil {
			t.Fatalf("retry setup error = %v\n%s", err, output)
		}
		if retry.gateway.listCalls < 2 {
			t.Fatalf("retry did not bypass failed cache; listCalls=%d", retry.gateway.listCalls)
		}

		edit := newHarness(t)
		prompts := &scriptedPrompter{responses: []promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "setup"},
			{kind: "input", value: "sk-bad-token-1234567890"},
			{kind: "input", value: edit.gateway.url()},
			{kind: "select", value: "edit"},
			{kind: "confirm", confirm: false},
			{kind: "input", value: altTestToken},
			{kind: "input", value: edit.gateway.url()},
			{kind: "confirm", confirm: true},
			{kind: "multiselect"},
			{kind: "confirm", confirm: true},
		}}
		output, err = edit.runWithPrompter(prompts)
		if err != nil {
			t.Fatalf("edit setup error = %v\n%s", err, output)
		}
		if !prompts.sawPromptTitle("Gateway validation failed") {
			t.Fatalf("gateway edit recovery prompt not shown; records=%+v", prompts.records)
		}
		if edit.gateway.listCalls < 2 {
			t.Fatalf("gateway edit did not revalidate; listCalls=%d", edit.gateway.listCalls)
		}
		assertFileContains(t, edit.fs, "/home/ada/.claude/settings.json", altTestToken)
		assertFileNotContains(t, edit.fs, "/home/ada/.claude/settings.json", "sk-bad-token-1234567890")
	})

	t.Run("manual advanced models unavailable and probe failure block writes", func(t *testing.T) {
		manual := newHarness(t)
		output, err := manual.runScripted([]promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "setup"},
			{kind: "input", value: testToken},
			{kind: "input", value: manual.gateway.url()},
			{kind: "confirm", confirm: false},
			{kind: "select", value: sonnetModel},
			{kind: "confirm", confirm: true},
			{kind: "select", value: haikuModel},
			{kind: "select", value: sonnetModel},
			{kind: "select", value: opusModel},
			{kind: "multiselect"},
			{kind: "confirm", confirm: true},
		})
		if err != nil {
			t.Fatalf("manual setup error = %v\n%s", err, output)
		}
		assertFileContains(t, manual.fs, "/home/ada/.claude/settings.json", "ANTHROPIC_DEFAULT_OPUS_MODEL")

		unavailable := newHarness(t, withGatewayOptions(t, fakeGatewayOptions{models: []string{"openai-compatible-model"}}))
		prompts := &scriptedPrompter{responses: []promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "setup"},
			{kind: "input", value: testToken},
			{kind: "input", value: unavailable.gateway.url()},
			{kind: "select", value: missingModel},
			{kind: "confirm", confirm: false},
			{kind: "select", value: "exit"},
		}}
		output, err = unavailable.runWithPrompter(prompts)
		if err != nil {
			t.Fatalf("unavailable setup error = %v\n%s", err, output)
		}
		foundUnavailablePrompt := false
		for _, record := range prompts.records {
			if record.title == "Selected model unavailable" {
				foundUnavailablePrompt = true
			}
		}
		if !foundUnavailablePrompt {
			t.Fatalf("unavailable model recovery prompt not shown; records=%+v", prompts.records)
		}
		if unavailable.fs.mutationOps() != 0 {
			t.Fatalf("unavailable model wrote filesystem %d time(s)", unavailable.fs.mutationOps())
		}

		probeFail := newHarness(t, withGatewayOptions(t, fakeGatewayOptions{
			probeFailures: map[string]gatewayResponse{
				sonnetModel: {status: http.StatusBadGateway, body: `{"detail":"probe failed"}`},
			},
		}))
		prompts = &scriptedPrompter{responses: []promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "setup"},
			{kind: "input", value: testToken},
			{kind: "input", value: probeFail.gateway.url()},
			{kind: "confirm", confirm: true},
			{kind: "select", value: "exit"},
		}}
		output, err = probeFail.runWithPrompter(prompts)
		if err != nil {
			t.Fatalf("probe-failure setup error = %v\n%s", err, output)
		}
		foundProbePrompt := false
		for _, record := range prompts.records {
			if record.title == "Model probe failed" {
				foundProbePrompt = true
			}
		}
		if !foundProbePrompt {
			t.Fatalf("probe failure recovery prompt not shown; records=%+v", prompts.records)
		}
		if probeFail.fs.mutationOps() != 0 {
			t.Fatalf("probe failure wrote filesystem %d time(s)", probeFail.fs.mutationOps())
		}

		chooseAfterUnavailable := newHarness(t)
		prompts = &scriptedPrompter{responses: []promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "setup"},
			{kind: "input", value: testToken},
			{kind: "input", value: chooseAfterUnavailable.gateway.url()},
			{kind: "confirm", confirm: false},
			{kind: "select", value: missingModel},
			{kind: "confirm", confirm: false},
			{kind: "select", value: "choose"},
			{kind: "confirm", confirm: true},
			{kind: "multiselect"},
			{kind: "confirm", confirm: true},
		}}
		output, err = chooseAfterUnavailable.runWithPrompter(prompts)
		if err != nil {
			t.Fatalf("choose-after-unavailable setup error = %v\n%s", err, output)
		}
		if !prompts.sawPromptTitle("Selected model unavailable") {
			t.Fatalf("unavailable choose recovery prompt not shown; records=%+v", prompts.records)
		}
		assertFileContains(t, chooseAfterUnavailable.fs, "/home/ada/.claude/settings.json", sonnetModel)

		chooseAfterProbe := newHarness(t, withGatewayOptions(t, fakeGatewayOptions{
			probeFailures: map[string]gatewayResponse{
				sonnetModel: {status: http.StatusBadGateway, body: `{"detail":"probe failed"}`},
			},
		}))
		prompts = &scriptedPrompter{responses: []promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "setup"},
			{kind: "input", value: testToken},
			{kind: "input", value: chooseAfterProbe.gateway.url()},
			{kind: "confirm", confirm: true},
			{kind: "select", value: "choose"},
			{kind: "confirm", confirm: false},
			{kind: "select", value: haikuModel},
			{kind: "confirm", confirm: false},
			{kind: "multiselect"},
			{kind: "confirm", confirm: true},
		}}
		output, err = chooseAfterProbe.runWithPrompter(prompts)
		if err != nil {
			t.Fatalf("choose-after-probe setup error = %v\n%s", err, output)
		}
		if !prompts.sawPromptTitle("Model probe failed") {
			t.Fatalf("probe choose recovery prompt not shown; records=%+v", prompts.records)
		}
		assertFileContains(t, chooseAfterProbe.fs, "/home/ada/.claude/settings.json", haikuModel)

		editAfterProbe := newHarness(t, withGatewayOptions(t, fakeGatewayOptions{
			probeFailures: map[string]gatewayResponse{
				sonnetModel: {status: http.StatusBadGateway, body: `{"detail":"probe failed"}`},
			},
		}))
		goodGateway := newFakeGateway(t, fakeGatewayOptions{models: recommendedModels})
		t.Cleanup(goodGateway.close)
		prompts = &scriptedPrompter{responses: []promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "setup"},
			{kind: "input", value: testToken},
			{kind: "input", value: editAfterProbe.gateway.url()},
			{kind: "confirm", confirm: true},
			{kind: "select", value: "edit"},
			{kind: "confirm", confirm: true},
			{kind: "input", value: goodGateway.url()},
			{kind: "confirm", confirm: true},
			{kind: "multiselect"},
			{kind: "confirm", confirm: true},
		}}
		output, err = editAfterProbe.runWithPrompter(prompts)
		if err != nil {
			t.Fatalf("edit-after-probe setup error = %v\n%s", err, output)
		}
		if !prompts.sawPromptTitle("Model probe failed") {
			t.Fatalf("probe edit recovery prompt not shown; records=%+v", prompts.records)
		}
		if goodGateway.probeCalls == 0 {
			t.Fatalf("model edit recovery did not use edited gateway")
		}
		assertFileContains(t, editAfterProbe.fs, "/home/ada/.claude/settings.json", goodGateway.url())
	})
}

func TestAcceptanceMacLinuxWriteScenarios(t *testing.T) {
	t.Run("fresh zsh setup writes POSIX exports", func(t *testing.T) {
		h := newHarness(t)
		output, err := h.runScripted(freshSetupResponses(h.gateway.url(), nil))
		if err != nil {
			t.Fatalf("Run() error = %v\n%s", err, output)
		}
		assertFileContains(t, h.fs, "/home/ada/.zshrc", "export ANTHROPIC_MODEL='"+sonnetModel+"'")
	})

	t.Run("macOS bash uses bash_profile and backs up existing file", func(t *testing.T) {
		h := newHarness(t, withPlatform("darwin", "/Users/ada", "/Users/ada/project"), withShell("/bin/bash"))
		h.fs.addFile("/Users/ada/.bash_profile", []byte("# existing\n"), 0o600)
		output, err := h.runScripted(freshSetupResponses(h.gateway.url(), nil))
		if err != nil {
			t.Fatalf("Run() error = %v\n%s", err, output)
		}
		assertFileContains(t, h.fs, "/Users/ada/.bash_profile", "export ANTHROPIC_MODEL='"+sonnetModel+"'")
		assertFileContains(t, h.fs, "/Users/ada/.bash_profile.llmgate.bak", "# existing")
	})

	t.Run("fish setup writes fish syntax", func(t *testing.T) {
		h := newHarness(t, withShell("/usr/bin/fish"))
		output, err := h.runScripted(freshSetupResponses(h.gateway.url(), nil))
		if err != nil {
			t.Fatalf("Run() error = %v\n%s", err, output)
		}
		assertFileContains(t, h.fs, "/home/ada/.config/fish/config.fish", "set -x ANTHROPIC_MODEL '"+sonnetModel+"'")
	})

	t.Run("updates preserve content and idempotent rerun skips writes", func(t *testing.T) {
		h := newHarness(t)
		h.fs.addFile("/home/ada/.claude/settings.json", []byte(`{"permissions":{"allow":["Read"]},"env":{"ANTHROPIC_AUTH_TOKEN":"`+altTestToken+`"}}`+"\n"), 0o600)
		h.fs.addFile("/home/ada/.zshrc", []byte("# keep me\nexport ANTHROPIC_MODEL='old-model'\n"), 0o600)
		output, err := h.runScripted([]promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "setup"},
			{kind: "confirm", confirm: false},
			{kind: "input", value: testToken},
			{kind: "input", value: h.gateway.url()},
			{kind: "confirm", confirm: true},
			{kind: "multiselect"},
			{kind: "confirm", confirm: true},
		})
		if err != nil {
			t.Fatalf("Run() error = %v\n%s", err, output)
		}
		assertFileContains(t, h.fs, "/home/ada/.claude/settings.json", "permissions")
		assertFileContains(t, h.fs, "/home/ada/.zshrc", "# keep me")
		assertFileContains(t, h.fs, "/home/ada/.zshrc.llmgate.bak", "old-model")

		h.resetCounts()
		output, err = h.runScripted(existingTokenSetupResponses(h.gateway.url(), nil))
		if err != nil {
			t.Fatalf("idempotent rerun error = %v\n%s", err, output)
		}
		assertContains(t, output, "status: skipped")
		if h.fs.mutationOps() != 0 {
			t.Fatalf("idempotent rerun mutated filesystem %d time(s)\n%s", h.fs.mutationOps(), output)
		}
	})

	t.Run("malformed Claude settings and dynamic shell assignment are safe", func(t *testing.T) {
		malformed := newHarness(t)
		malformed.fs.addFile("/home/ada/.claude/settings.json", []byte(`{"env":`), 0o600)
		output, err := malformed.runScripted(freshSetupResponses(malformed.gateway.url(), nil))
		if err == nil {
			t.Fatalf("Run() succeeded, want malformed settings error\n%s", output)
		}
		if malformed.fs.mutationOps() != 0 {
			t.Fatalf("malformed settings were overwritten; mutations=%d", malformed.fs.mutationOps())
		}

		dynamic := newHarness(t)
		dynamic.fs.addFile("/home/ada/.zshrc", []byte("export ANTHROPIC_MODEL=\"$(choose_model)\"\n"), 0o600)
		output, err = dynamic.runScripted(freshSetupResponses(dynamic.gateway.url(), nil))
		if err != nil {
			t.Fatalf("dynamic shell setup error = %v\n%s", err, output)
		}
		assertContains(t, output, "requires manual review")
		assertFileContains(t, dynamic.fs, "/home/ada/.zshrc", "$(choose_model)")
	})
}

func TestAcceptanceWindowsIDEScenarios(t *testing.T) {
	t.Run("Windows setup writes and rerun skips user environment", func(t *testing.T) {
		h := newHarness(t, withPlatform("windows", `C:\Users\Ada`, `C:\Users\Ada\project`))
		h.env.values["APPDATA"] = `C:\Users\Ada\AppData\Roaming`
		output, err := h.runScripted(freshSetupResponses(h.gateway.url(), nil))
		if err != nil {
			t.Fatalf("Run() error = %v\n%s", err, output)
		}
		if h.winEnv.values[core.VarAnthropicAuthToken] != testToken {
			t.Fatalf("Windows user env token not written")
		}
		assertContains(t, output, "No file backup is created for Windows user environment updates")
		h.resetCounts()
		output, err = h.runScripted(existingTokenSetupResponses(h.gateway.url(), nil))
		if err != nil {
			t.Fatalf("Windows idempotent rerun error = %v\n%s", err, output)
		}
		if h.winEnv.setOps != 0 {
			t.Fatalf("unchanged Windows user env set %d value(s)", h.winEnv.setOps)
		}
	})

	t.Run("IDE targets require directories and preserve settings", func(t *testing.T) {
		noIDE := newHarness(t)
		output, err := noIDE.runScripted([]promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "review"},
		})
		if err != nil {
			t.Fatalf("review error = %v\n%s", err, output)
		}
		assertNotContains(t, output, "VS Code settings")
		assertNotContains(t, output, "Cursor settings")

		withIDE := newHarness(t)
		withIDE.fs.addDir("/home/ada/.config/Code/User")
		withIDE.fs.addDir("/home/ada/.config/Cursor/User")
		withIDE.fs.addFile("/home/ada/.config/Code/User/settings.json", []byte(`{"editor.tabSize":2,"claudeCode.environmentVariables":[{"name":"UNRELATED","value":"keep"}]}`+"\n"), 0o600)
		output, err = withIDE.runScripted(freshSetupResponses(withIDE.gateway.url(), nil))
		if err != nil {
			t.Fatalf("IDE setup error = %v\n%s", err, output)
		}
		assertFileContains(t, withIDE.fs, "/home/ada/.config/Code/User/settings.json", "claudeCode.selectedModel")
		assertFileContains(t, withIDE.fs, "/home/ada/.config/Code/User/settings.json", "UNRELATED")
		assertFileContains(t, withIDE.fs, "/home/ada/.config/Cursor/User/settings.json", "claudeCode.environmentVariables")
	})

	t.Run("IDE drift and unavailable selected model warn", func(t *testing.T) {
		h := newHarness(t)
		h.fs.addFile("/home/ada/.claude/settings.json", fullClaudeSettings(h.gateway.url()), 0o600)
		h.fs.addDir("/home/ada/.config/Code/User")
		h.fs.addFile("/home/ada/.config/Code/User/settings.json", ideSettings(missingModel, map[string]string{core.VarAnthropicModel: missingModel}), 0o600)
		output, err := h.runScripted([]promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "review"},
		})
		if err != nil {
			t.Fatalf("IDE review error = %v\n%s", err, output)
		}
		assertContains(t, output, "IDE Config")
		assertContains(t, output, "IDE Config Validation")
		assertContains(t, output, "model is unavailable")
	})
}

func TestAcceptanceProjectRepairAndFinalDiagnosticsScenarios(t *testing.T) {
	t.Run("project overrides warn and are not write targets", func(t *testing.T) {
		h := newHarness(t)
		h.fs.addFile("/home/ada/.claude/settings.json", fullClaudeSettings(h.gateway.url()), 0o600)
		h.fs.addFile("/home/ada/project/.claude/settings.local.json", projectSettings(map[string]string{
			core.VarAnthropicAuthToken: leakedToken,
			core.VarAnthropicModel:     missingModel,
		}), 0o600)
		read, err := config.Read(h.system(), true)
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		for _, target := range read.WriteTargets {
			if strings.Contains(string(target.Kind), "project") {
				t.Fatalf("project setting offered as write target: %+v", target)
			}
		}
		output, err := h.runScripted([]promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "review"},
		})
		if err != nil {
			t.Fatalf("project review error = %v\n%s", err, output)
		}
		assertContains(t, output, "Project Overrides")
		assertContains(t, output, "Project Config Validation")
		assertNoSecretLeak(t, output, leakedToken)

		malformed := newHarness(t)
		malformed.fs.addFile("/home/ada/.claude/settings.json", fullClaudeSettings(malformed.gateway.url()), 0o600)
		malformed.fs.addFile("/home/ada/project/.claude/settings.json", []byte(`{"env":`), 0o600)
		output, err = malformed.runScripted([]promptResponse{{kind: "confirm", confirm: true}, {kind: "select", value: "review"}})
		if err != nil {
			t.Fatalf("malformed project review error = %v\n%s", err, output)
		}
		assertContains(t, output, "Config Sources")
		assertContains(t, output, "WARN")
	})

	t.Run("repair updates simple stale shell models and skips cancellation", func(t *testing.T) {
		h := repairHarness(t)
		output, err := h.runScripted([]promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "repair"},
			{kind: "confirm", confirm: true},
		})
		if err != nil {
			t.Fatalf("repair error = %v\n%s", err, output)
		}
		assertFileContains(t, h.fs, "/home/ada/.zshrc", "export ANTHROPIC_MODEL='"+sonnetModel+"'")
		assertFileContains(t, h.fs, "/home/ada/.zshrc", "# export ANTHROPIC_MODEL='"+staleModel+"'")

		hidden := newHarness(t)
		hidden.fs.addFile("/home/ada/.claude/settings.json", fullClaudeSettingsWithModel(hidden.gateway.url(), staleModel), 0o600)
		hidden.env.values[core.VarAnthropicAuthToken] = testToken
		hidden.env.values[core.VarAnthropicBaseURL] = hidden.gateway.url()
		hidden.env.values[core.VarAnthropicModel] = sonnetModel
		hidden.env.values[core.VarAnthropicDefaultHaikuModel] = haikuModel
		hidden.env.values[core.VarAnthropicDefaultSonnetModel] = sonnetModel
		hidden.env.values[core.VarAnthropicDefaultOpusModel] = opusModel
		prompts := &scriptedPrompter{responses: []promptResponse{{kind: "confirm", confirm: true}, {kind: "select", value: "exit"}}}
		output, err = hidden.runWithPrompter(prompts)
		if err != nil {
			t.Fatalf("hidden repair review error = %v\n%s", err, output)
		}
		if prompts.sawSelectOption("Repair warnings") {
			t.Fatalf("Repair warnings appeared for non-shell stale model")
		}

		cancelled := repairHarness(t)
		output, err = cancelled.runScripted([]promptResponse{
			{kind: "confirm", confirm: true},
			{kind: "select", value: "repair"},
			{kind: "confirm", err: wizard.ErrCanceled},
		})
		if err != nil {
			t.Fatalf("cancelled repair error = %v\n%s", err, output)
		}
		if cancelled.fs.mutationOps() != 0 {
			t.Fatalf("cancelled repair wrote filesystem %d time(s)", cancelled.fs.mutationOps())
		}
	})

	t.Run("final diagnostics labels OK WARN and FAIL outcomes", func(t *testing.T) {
		ok := newHarness(t)
		output, err := ok.runScripted(freshSetupResponses(ok.gateway.url(), nil))
		if err != nil {
			t.Fatalf("OK setup error = %v\n%s", err, output)
		}
		assertContains(t, output, "Configured")

		warn := newHarness(t)
		warn.fs.addFile("/home/ada/project/.claude/settings.json", projectSettings(map[string]string{core.VarAnthropicModel: missingModel}), 0o600)
		output, err = warn.runScripted(freshSetupResponses(warn.gateway.url(), nil))
		if err != nil {
			t.Fatalf("WARN setup error = %v\n%s", err, output)
		}
		assertContains(t, output, "Configured with warnings")
		assertContains(t, output, "Restart your terminal and IDE")

		fail := newHarness(t)
		fail.fs.afterMove = func(finalPath string) {
			if finalPath == "/home/ada/.claude/settings.json" {
				fail.fs.files[finalPath] = []byte(`{"env":`)
			}
		}
		output, err = fail.runScripted(freshSetupResponses(fail.gateway.url(), nil))
		if !errors.Is(err, wizard.ErrSetupIncomplete) {
			t.Fatalf("FAIL setup error = %v, want ErrSetupIncomplete\n%s", err, output)
		}
		assertContains(t, output, "Setup incomplete")
	})
}

func freshSetupResponses(baseURL string, targets []string) []promptResponse {
	return []promptResponse{
		{kind: "confirm", confirm: true},
		{kind: "select", value: "setup"},
		{kind: "input", value: testToken},
		{kind: "input", value: baseURL},
		{kind: "confirm", confirm: true},
		{kind: "multiselect", values: targets},
		{kind: "confirm", confirm: true},
	}
}

func existingTokenSetupResponses(baseURL string, targets []string) []promptResponse {
	return []promptResponse{
		{kind: "confirm", confirm: true},
		{kind: "select", value: "setup"},
		{kind: "confirm", confirm: true},
		{kind: "input", value: baseURL},
		{kind: "confirm", confirm: true},
		{kind: "multiselect", values: targets},
		{kind: "confirm", confirm: true},
	}
}

func repairHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.fs.addFile("/home/ada/.claude/settings.json", fullClaudeSettings(h.gateway.url()), 0o600)
	h.fs.addFile("/home/ada/.zshrc", []byte(
		"# export ANTHROPIC_MODEL='"+staleModel+"'\n"+
			"export ANTHROPIC_MODEL='"+staleModel+"'\n",
	), 0o600)
	return h
}

func fullClaudeSettings(baseURL string) []byte {
	return fullClaudeSettingsWithModel(baseURL, sonnetModel)
}

func fullClaudeSettingsWithModel(baseURL, model string) []byte {
	return projectSettings(map[string]string{
		core.VarAnthropicAuthToken:          testToken,
		core.VarAnthropicBaseURL:            baseURL,
		core.VarAnthropicModel:              model,
		core.VarAnthropicDefaultHaikuModel:  haikuModel,
		core.VarAnthropicDefaultSonnetModel: sonnetModel,
		core.VarAnthropicDefaultOpusModel:   opusModel,
	})
}

func projectSettings(values map[string]string) []byte {
	data, err := json.MarshalIndent(map[string]any{"env": values}, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(data, '\n')
}

func ideSettings(selectedModel string, values map[string]string) []byte {
	entries := make([]map[string]string, 0, len(values))
	for name, value := range values {
		entries = append(entries, map[string]string{"name": name, "value": value})
	}
	data, err := json.MarshalIndent(map[string]any{
		"claudeCode.selectedModel":        selectedModel,
		"claudeCode.environmentVariables": entries,
	}, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(data, '\n')
}
