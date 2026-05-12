package settings

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/r13v/llmgate/internal/core"
	"github.com/tailscale/hujson"
)

func TestParseClaudeReadsManagedStringEnvOnly(t *testing.T) {
	input := []byte(`{
		// comments are accepted
		"env": {
			"ANTHROPIC_AUTH_TOKEN": "sk-testtoken123456",
			"ANTHROPIC_BASE_URL": "https://gateway.example.com",
			"ANTHROPIC_MODEL": 42,
			"PATH": "/usr/bin"
		}
	}`)

	settings, err := ParseClaude(input)
	if err != nil {
		t.Fatalf("ParseClaude() error = %v", err)
	}

	if got := settings.Env[core.VarAnthropicAuthToken]; got != "sk-testtoken123456" {
		t.Fatalf("auth token = %q, want sk-testtoken123456", got)
	}
	if got := settings.Env[core.VarAnthropicBaseURL]; got != "https://gateway.example.com" {
		t.Fatalf("base URL = %q, want https://gateway.example.com", got)
	}
	if _, ok := settings.Env[core.VarAnthropicModel]; ok {
		t.Fatalf("non-string managed model should not be read")
	}
	if _, ok := settings.Env["PATH"]; ok {
		t.Fatalf("unmanaged env value should not be read")
	}
}

func TestParseRejectsMalformedJSONCAndNonObjectRoot(t *testing.T) {
	tests := []struct {
		name  string
		parse func([]byte) error
		input string
	}{
		{
			name: "claude malformed redacts token",
			parse: func(data []byte) error {
				_, err := ParseClaude(data)
				return err
			},
			input: `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-secretvalue123456",`,
		},
		{
			name: "claude non-object root",
			parse: func(data []byte) error {
				_, err := ParseClaude(data)
				return err
			},
			input: `[]`,
		},
		{
			name: "ide malformed redacts token",
			parse: func(data []byte) error {
				_, err := ParseIDE(data)
				return err
			},
			input: `{"claudeCode.environmentVariables":[{"name":"ANTHROPIC_AUTH_TOKEN","value":"sk-secretvalue123456"},`,
		},
		{
			name: "ide non-object root",
			parse: func(data []byte) error {
				_, err := ParseIDE(data)
				return err
			},
			input: `"settings"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.parse([]byte(tt.input))
			if err == nil {
				t.Fatalf("parse error = nil, want error")
			}
			if strings.Contains(err.Error(), "sk-secretvalue123456") {
				t.Fatalf("error leaked secret: %v", err)
			}
		})
	}
}

func TestUpsertClaudePreservesUnrelatedSettingsCommentsAndIsIdempotent(t *testing.T) {
	input := readFixture(t, "claude_comments.jsonc")
	values := map[string]string{
		core.VarAnthropicAuthToken: "sk-newtoken123456",
		core.VarAnthropicBaseURL:   "https://gateway.example.com",
		core.VarAnthropicModel:     "claude-primary",
	}

	output, err := UpsertClaude(input, values)
	if err != nil {
		t.Fatalf("UpsertClaude() error = %v", err)
	}
	if !strings.HasSuffix(string(output), "\n") {
		t.Fatalf("output should have trailing newline")
	}
	if !strings.Contains(string(output), "// unrelated top-level settings") ||
		!strings.Contains(string(output), "// unrelated environment values") {
		t.Fatalf("output did not preserve JSONC comments:\n%s", output)
	}

	decoded := decodeObject(t, output)
	if got := decoded["theme"]; got != "dark" {
		t.Fatalf("theme = %v, want dark", got)
	}
	env := decodedObject(t, decoded["env"])
	if got := env["PATH"]; got != "/usr/bin" {
		t.Fatalf("env PATH = %v, want /usr/bin", got)
	}
	if got := env[core.VarAnthropicAuthToken]; got != "sk-newtoken123456" {
		t.Fatalf("env token = %v, want sk-newtoken123456", got)
	}
	if got := env[core.VarAnthropicBaseURL]; got != "https://gateway.example.com" {
		t.Fatalf("env base URL = %v, want https://gateway.example.com", got)
	}
	if got := env[core.VarAnthropicModel]; got != "claude-primary" {
		t.Fatalf("env model = %v, want claude-primary", got)
	}

	second, err := UpsertClaude(output, values)
	if err != nil {
		t.Fatalf("second UpsertClaude() error = %v", err)
	}
	if string(second) != string(output) {
		t.Fatalf("UpsertClaude() should be idempotent\nfirst:\n%s\nsecond:\n%s", output, second)
	}
}

func TestUpsertClaudeLeavesSemanticallyEqualSettingsUntouched(t *testing.T) {
	input := []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://gateway.example.com"}}`)

	output, err := UpsertClaude(input, map[string]string{
		core.VarAnthropicBaseURL: "https://gateway.example.com",
	})
	if err != nil {
		t.Fatalf("UpsertClaude() error = %v", err)
	}
	if string(output) != string(input) {
		t.Fatalf("semantically equal settings were rewritten:\n%s", output)
	}
}

func TestUpsertClaudeCreatesMissingFileAndRejectsMalformedEnv(t *testing.T) {
	created, err := UpsertClaude(nil, map[string]string{
		core.VarAnthropicBaseURL: "https://gateway.example.com",
	})
	if err != nil {
		t.Fatalf("UpsertClaude(nil) error = %v", err)
	}
	decoded := decodeObject(t, created)
	env := decodedObject(t, decoded["env"])
	if got := env[core.VarAnthropicBaseURL]; got != "https://gateway.example.com" {
		t.Fatalf("created env base URL = %v, want https://gateway.example.com", got)
	}

	_, err = UpsertClaude([]byte(`{"env": "not-object"}`), map[string]string{
		core.VarAnthropicBaseURL: "https://gateway.example.com",
	})
	if err == nil {
		t.Fatalf("UpsertClaude() error = nil, want malformed env error")
	}
}

func TestParseIDEReadsSelectedModelAndManagedStringEnvironmentOnly(t *testing.T) {
	input := []byte(`{
		"claudeCode.selectedModel": "claude-primary",
		"claudeCode.environmentVariables": [
			{ "name": "ANTHROPIC_AUTH_TOKEN", "value": "sk-testtoken123456" },
			{ "name": "ANTHROPIC_BASE_URL", "value": "https://gateway.example.com" },
			{ "name": "ANTHROPIC_MODEL", "value": 42 },
			{ "name": "PATH", "value": "/usr/bin" },
			{ "name": 123, "value": "ignored" }
		]
	}`)

	settings, err := ParseIDE(input)
	if err != nil {
		t.Fatalf("ParseIDE() error = %v", err)
	}

	if !settings.HasSelectedModel {
		t.Fatalf("HasSelectedModel = false, want true")
	}
	if settings.SelectedModel != "claude-primary" {
		t.Fatalf("SelectedModel = %q, want claude-primary", settings.SelectedModel)
	}
	if got := settings.Environment[core.VarAnthropicAuthToken]; got != "sk-testtoken123456" {
		t.Fatalf("auth token = %q, want sk-testtoken123456", got)
	}
	if got := settings.Environment[core.VarAnthropicBaseURL]; got != "https://gateway.example.com" {
		t.Fatalf("base URL = %q, want https://gateway.example.com", got)
	}
	if _, ok := settings.Environment[core.VarAnthropicModel]; ok {
		t.Fatalf("non-string managed model should not be read")
	}
	if _, ok := settings.Environment["PATH"]; ok {
		t.Fatalf("unmanaged environment value should not be read")
	}
}

func TestUpsertIDEPreservesUnrelatedSettingsEntriesCommentsAndIsIdempotent(t *testing.T) {
	input := readFixture(t, "ide_comments.jsonc")
	values := map[string]string{
		core.VarAnthropicAuthToken:         "sk-newtoken123456",
		core.VarAnthropicBaseURL:           "https://gateway.example.com",
		core.VarAnthropicDefaultHaikuModel: "claude-haiku",
	}

	output, err := UpsertIDE(input, "claude-primary", values)
	if err != nil {
		t.Fatalf("UpsertIDE() error = %v", err)
	}
	if !strings.HasSuffix(string(output), "\n") {
		t.Fatalf("output should have trailing newline")
	}
	if !strings.Contains(string(output), "// unrelated IDE settings") ||
		!strings.Contains(string(output), "// unrelated entries") {
		t.Fatalf("output did not preserve JSONC comments:\n%s", output)
	}

	decoded := decodeObject(t, output)
	if got := decoded["editor.fontSize"]; got != float64(14) {
		t.Fatalf("editor.fontSize = %v, want 14", got)
	}
	if got := decoded[ideSelectedModelKey]; got != "claude-primary" {
		t.Fatalf("selected model = %v, want claude-primary", got)
	}

	entries := decodedArray(t, decoded[ideEnvironmentKey])
	valuesByName := map[string]string{}
	for _, entry := range entries {
		obj := decodedObject(t, entry)
		name, nameOK := obj["name"].(string)
		value, valueOK := obj["value"].(string)
		if nameOK && valueOK {
			valuesByName[name] = value
		}
	}
	if got := valuesByName["PATH"]; got != "/usr/bin" {
		t.Fatalf("PATH entry = %q, want /usr/bin", got)
	}
	if got := valuesByName[core.VarAnthropicAuthToken]; got != "sk-newtoken123456" {
		t.Fatalf("token entry = %q, want sk-newtoken123456", got)
	}
	if got := valuesByName[core.VarAnthropicBaseURL]; got != "https://gateway.example.com" {
		t.Fatalf("base URL entry = %q, want https://gateway.example.com", got)
	}
	if got := valuesByName[core.VarAnthropicDefaultHaikuModel]; got != "claude-haiku" {
		t.Fatalf("haiku entry = %q, want claude-haiku", got)
	}

	second, err := UpsertIDE(output, "claude-primary", values)
	if err != nil {
		t.Fatalf("second UpsertIDE() error = %v", err)
	}
	if string(second) != string(output) {
		t.Fatalf("UpsertIDE() should be idempotent\nfirst:\n%s\nsecond:\n%s", output, second)
	}
}

func TestUpsertIDELeavesSemanticallyEqualSettingsUntouched(t *testing.T) {
	input := []byte(`{"claudeCode.selectedModel":"claude-primary","claudeCode.environmentVariables":[{"name":"ANTHROPIC_BASE_URL","value":"https://gateway.example.com"}]}`)

	output, err := UpsertIDE(input, "claude-primary", map[string]string{
		core.VarAnthropicBaseURL: "https://gateway.example.com",
	})
	if err != nil {
		t.Fatalf("UpsertIDE() error = %v", err)
	}
	if string(output) != string(input) {
		t.Fatalf("semantically equal IDE settings were rewritten:\n%s", output)
	}
}

func TestUpsertIDECreatesMissingFileAndRejectsMalformedEnvironmentVariables(t *testing.T) {
	created, err := UpsertIDE(nil, "claude-primary", map[string]string{
		core.VarAnthropicBaseURL: "https://gateway.example.com",
	})
	if err != nil {
		t.Fatalf("UpsertIDE(nil) error = %v", err)
	}
	decoded := decodeObject(t, created)
	if got := decoded[ideSelectedModelKey]; got != "claude-primary" {
		t.Fatalf("selected model = %v, want claude-primary", got)
	}
	entries := decodedArray(t, decoded[ideEnvironmentKey])
	if len(entries) != 1 {
		t.Fatalf("len(environmentVariables) = %d, want 1", len(entries))
	}

	_, err = UpsertIDE([]byte(`{"claudeCode.environmentVariables": "not-array"}`), "claude-primary", map[string]string{
		core.VarAnthropicBaseURL: "https://gateway.example.com",
	})
	if err == nil {
		t.Fatalf("UpsertIDE() error = nil, want malformed environmentVariables error")
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", name, err)
	}
	return data
}

func decodeObject(t *testing.T, data []byte) map[string]any {
	t.Helper()
	standard, err := hujson.Standardize(append([]byte(nil), data...))
	if err != nil {
		t.Fatalf("hujson.Standardize() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(standard, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v\ninput:\n%s", err, standard)
	}
	return decoded
}

func decodedObject(t *testing.T, value any) map[string]any {
	t.Helper()
	obj, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value = %#v, want object", value)
	}
	return obj
}

func decodedArray(t *testing.T, value any) []any {
	t.Helper()
	array, ok := value.([]any)
	if !ok {
		t.Fatalf("value = %#v, want array", value)
	}
	return array
}
