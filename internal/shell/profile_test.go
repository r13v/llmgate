package shell

import (
	"os"
	"strings"
	"testing"

	"github.com/r13v/llmgate/internal/core"
)

func TestParsePOSIXProfilesReadSimpleAssignmentsAndIgnoreComments(t *testing.T) {
	input := []byte(strings.Join([]string{
		"# export ANTHROPIC_AUTH_TOKEN='sk-commented123456'",
		"export ANTHROPIC_AUTH_TOKEN='sk-active123456'",
		"ANTHROPIC_BASE_URL=https://gateway.example.com # not exported",
		"export PATH='/usr/bin'",
		"",
	}, "\n"))

	for _, name := range []string{"zsh", "bash"} {
		t.Run(name, func(t *testing.T) {
			profile, err := ParseProfile(input, SyntaxPOSIX)
			if err != nil {
				t.Fatalf("ParseProfile() error = %v", err)
			}
			assertProfileValue(t, profile, core.VarAnthropicAuthToken, "sk-active123456", 2)
			assertProfileValueMissing(t, profile, core.VarAnthropicBaseURL)
			if _, ok := profile.Values["PATH"]; ok {
				t.Fatalf("unmanaged PATH should not be read")
			}
			if len(profile.Issues) != 0 {
				t.Fatalf("Issues = %#v, want none", profile.Issues)
			}
		})
	}
}

func TestParseDetectsDuplicateDynamicAndComplexPOSIXAssignments(t *testing.T) {
	input := []byte(strings.Join([]string{
		"export ANTHROPIC_MODEL='claude-old'",
		"ANTHROPIC_MODEL=claude-new",
		"export ANTHROPIC_DEFAULT_HAIKU_MODEL=$(pick-haiku)",
		"declare -x ANTHROPIC_DEFAULT_SONNET_MODEL='claude-sonnet'",
		"export ANTHROPIC_DEFAULT_OPUS_MODEL",
		"export ANTHROPIC_BASE_URL=https://one.example.com ANTHROPIC_AUTH_TOKEN=sk-token123456",
		"",
	}, "\n"))

	profile, err := ParseProfile(input, SyntaxPOSIX)
	if err != nil {
		t.Fatalf("ParseProfile() error = %v", err)
	}
	assertProfileValue(t, profile, core.VarAnthropicModel, "claude-new", 2)
	assertIssue(t, profile.Duplicates, IssueDuplicate, core.VarAnthropicModel, 0)
	assertIssue(t, profile.Manual, IssueDynamic, core.VarAnthropicDefaultHaikuModel, 3)
	assertIssue(t, profile.Manual, IssueComplex, core.VarAnthropicDefaultSonnetModel, 4)
	assertIssue(t, profile.Manual, IssueComplex, core.VarAnthropicDefaultOpusModel, 5)
	assertIssue(t, profile.Manual, IssueComplex, core.VarAnthropicBaseURL, 6)
	assertIssue(t, profile.Manual, IssueComplex, core.VarAnthropicAuthToken, 6)
}

func TestParseOnlyExportedShellAssignmentsAreEffective(t *testing.T) {
	posixProfile, err := ParseProfile([]byte(strings.Join([]string{
		"ANTHROPIC_AUTH_TOKEN='sk-local123456'",
		"export ANTHROPIC_BASE_URL='https://old.example.com'",
		"ANTHROPIC_BASE_URL='https://new.example.com'",
		"",
	}, "\n")), SyntaxPOSIX)
	if err != nil {
		t.Fatalf("ParseProfile(POSIX) error = %v", err)
	}
	assertProfileValueMissing(t, posixProfile, core.VarAnthropicAuthToken)
	assertProfileValue(t, posixProfile, core.VarAnthropicBaseURL, "https://new.example.com", 3)

	fishProfile, err := ParseProfile([]byte(strings.Join([]string{
		"set ANTHROPIC_AUTH_TOKEN 'sk-local123456'",
		"set -l ANTHROPIC_BASE_URL 'https://local.example.com'",
		"set --export ANTHROPIC_MODEL 'claude-old'",
		"set ANTHROPIC_MODEL 'claude-sonnet'",
		"",
	}, "\n")), SyntaxFish)
	if err != nil {
		t.Fatalf("ParseProfile(fish) error = %v", err)
	}
	assertProfileValueMissing(t, fishProfile, core.VarAnthropicAuthToken)
	assertProfileValueMissing(t, fishProfile, core.VarAnthropicBaseURL)
	assertProfileValue(t, fishProfile, core.VarAnthropicModel, "claude-sonnet", 4)
	assertIssue(t, fishProfile.Duplicates, IssueDuplicate, core.VarAnthropicModel, 0)
	assertIssue(t, fishProfile.Manual, IssueComplex, core.VarAnthropicBaseURL, 2)
}

func TestParsePOSIXUnexportAndUnsetClearEffectiveValues(t *testing.T) {
	input := []byte(strings.Join([]string{
		"export ANTHROPIC_AUTH_TOKEN='sk-exported123456'",
		"export -n ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_AUTH_TOKEN='sk-local123456'",
		"export ANTHROPIC_BASE_URL='https://old.example.com'",
		"unset ANTHROPIC_BASE_URL",
		"export ANTHROPIC_MODEL='claude-old'",
		"unset -v ANTHROPIC_MODEL",
		"export ANTHROPIC_DEFAULT_HAIKU_MODEL='claude-old-haiku'",
		"export -n ANTHROPIC_DEFAULT_HAIKU_MODEL='claude-local-haiku'",
		"",
	}, "\n"))

	profile, err := ParseProfile(input, SyntaxPOSIX)
	if err != nil {
		t.Fatalf("ParseProfile() error = %v", err)
	}
	assertProfileValueMissing(t, profile, core.VarAnthropicAuthToken)
	assertProfileValueMissing(t, profile, core.VarAnthropicBaseURL)
	assertProfileValueMissing(t, profile, core.VarAnthropicModel)
	assertProfileValueMissing(t, profile, core.VarAnthropicDefaultHaikuModel)
	if len(profile.Issues) != 0 {
		t.Fatalf("Issues = %#v, want none", profile.Issues)
	}
}

func TestParseFishEraseClearsEffectiveValues(t *testing.T) {
	input := []byte(strings.Join([]string{
		"set -x ANTHROPIC_AUTH_TOKEN 'sk-exported123456'",
		"set -e ANTHROPIC_AUTH_TOKEN",
		"set -x ANTHROPIC_BASE_URL 'https://old.example.com'",
		"set --erase ANTHROPIC_BASE_URL",
		"",
	}, "\n"))

	profile, err := ParseProfile(input, SyntaxFish)
	if err != nil {
		t.Fatalf("ParseProfile() error = %v", err)
	}
	assertProfileValueMissing(t, profile, core.VarAnthropicAuthToken)
	assertProfileValueMissing(t, profile, core.VarAnthropicBaseURL)
	if len(profile.Issues) != 0 {
		t.Fatalf("Issues = %#v, want none", profile.Issues)
	}
}

func TestUpsertPOSIXUpdatesPreservesAppendsAndIsIdempotent(t *testing.T) {
	input := []byte(strings.Join([]string{
		"# shell setup",
		"# export ANTHROPIC_AUTH_TOKEN='sk-commented123456'",
		"export ANTHROPIC_AUTH_TOKEN='sk-oldtoken123456' # token comment",
		"ANTHROPIC_BASE_URL=https://old.example.com",
		"export ANTHROPIC_MODEL=$(choose-model)",
		"export PATH='/usr/bin'",
		"",
	}, "\n"))
	values := map[string]string{
		core.VarAnthropicAuthToken: "sk-new'token\\123456",
		core.VarAnthropicBaseURL:   "https://gateway.example.com/v1#fragment",
		core.VarAnthropicModel:     "claude-primary",
	}

	output, result, err := UpsertProfile(input, SyntaxPOSIX, values, ModeSetup)
	if err != nil {
		t.Fatalf("UpsertProfile() error = %v", err)
	}
	text := string(output)
	assertContains(t, text, "# shell setup")
	assertContains(t, text, "# export ANTHROPIC_AUTH_TOKEN='sk-commented123456'")
	assertContains(t, text, "export ANTHROPIC_AUTH_TOKEN='sk-new'\\''token\\123456' # token comment")
	assertContains(t, text, "export ANTHROPIC_BASE_URL='https://gateway.example.com/v1#fragment'")
	assertContains(t, text, "export ANTHROPIC_MODEL=$(choose-model)")
	assertContains(t, text, "export PATH='/usr/bin'")
	assertContains(t, text, "export ANTHROPIC_MODEL='claude-primary'")
	if !result.Changed {
		t.Fatalf("Changed = false, want true")
	}
	assertIssue(t, result.Skipped, IssueDynamic, core.VarAnthropicModel, 5)
	assertProfileValue(t, result.Profile, core.VarAnthropicModel, "claude-primary", 7)

	second, _, err := UpsertProfile(output, SyntaxPOSIX, values, ModeSetup)
	if err != nil {
		t.Fatalf("second UpsertProfile() error = %v", err)
	}
	if string(second) != text {
		t.Fatalf("UpsertProfile() should be idempotent\nfirst:\n%s\nsecond:\n%s", text, second)
	}
}

func TestUpsertPOSIXLeavesSemanticallyEqualAssignmentsUntouched(t *testing.T) {
	input := []byte("export ANTHROPIC_BASE_URL=https://gateway.example.com\n")
	values := map[string]string{
		core.VarAnthropicBaseURL: "https://gateway.example.com",
	}

	output, result, err := UpsertProfile(input, SyntaxPOSIX, values, ModeSetup)
	if err != nil {
		t.Fatalf("UpsertProfile() error = %v", err)
	}
	if string(output) != string(input) {
		t.Fatalf("semantically equal profile was rewritten:\n%s", output)
	}
	if result.Changed {
		t.Fatalf("Changed = true, want false")
	}
}

func TestUpsertRepairModeUpdatesOnlyExistingSimpleAssignments(t *testing.T) {
	input := []byte("export ANTHROPIC_MODEL='claude-old'\n")
	values := map[string]string{
		core.VarAnthropicBaseURL: "https://gateway.example.com",
		core.VarAnthropicModel:   "claude-new",
	}

	output, result, err := UpsertProfile(input, SyntaxPOSIX, values, ModeRepair)
	if err != nil {
		t.Fatalf("UpsertProfile repair error = %v", err)
	}
	text := string(output)
	assertContains(t, text, "export ANTHROPIC_MODEL='claude-new'")
	assertNotContains(t, text, core.VarAnthropicBaseURL)
	assertProfileValue(t, result.Profile, core.VarAnthropicModel, "claude-new", 1)
}

func TestUpsertSetupAppendsWhenLaterDynamicAssignmentWouldWin(t *testing.T) {
	input := []byte(strings.Join([]string{
		"export ANTHROPIC_MODEL='claude-old'",
		"export ANTHROPIC_MODEL=$(choose-model)",
		"",
	}, "\n"))
	values := map[string]string{
		core.VarAnthropicModel: "claude-new",
	}

	output, result, err := UpsertProfile(input, SyntaxPOSIX, values, ModeSetup)
	if err != nil {
		t.Fatalf("UpsertProfile() error = %v", err)
	}

	text := string(output)
	assertContains(t, text, "export ANTHROPIC_MODEL='claude-new'")
	assertContains(t, text, "export ANTHROPIC_MODEL=$(choose-model)")
	assertIssue(t, result.Skipped, IssueDynamic, core.VarAnthropicModel, 2)
	assertProfileValue(t, result.Profile, core.VarAnthropicModel, "claude-new", 3)
}

func TestParseAndUpsertFishProfile(t *testing.T) {
	input := []byte(strings.Join([]string{
		"# set -x ANTHROPIC_AUTH_TOKEN 'sk-commented123456'",
		"set -x ANTHROPIC_AUTH_TOKEN 'sk-oldtoken123456'",
		"set -gx ANTHROPIC_BASE_URL 'https://old.example.com' # base comment",
		"set -x ANTHROPIC_DEFAULT_HAIKU_MODEL (choose-haiku)",
		"set -x PATH /usr/bin",
		"",
	}, "\n"))

	profile, err := ParseProfile(input, SyntaxFish)
	if err != nil {
		t.Fatalf("ParseProfile() error = %v", err)
	}
	assertProfileValue(t, profile, core.VarAnthropicAuthToken, "sk-oldtoken123456", 2)
	assertProfileValue(t, profile, core.VarAnthropicBaseURL, "https://old.example.com", 3)
	assertIssue(t, profile.Manual, IssueDynamic, core.VarAnthropicDefaultHaikuModel, 4)

	values := map[string]string{
		core.VarAnthropicAuthToken:         "sk-new'token\\123456",
		core.VarAnthropicBaseURL:           "https://gateway.example.com",
		core.VarAnthropicDefaultHaikuModel: "claude-haiku",
	}
	output, result, err := UpsertProfile(input, SyntaxFish, values, ModeSetup)
	if err != nil {
		t.Fatalf("UpsertProfile() error = %v", err)
	}
	text := string(output)
	assertContains(t, text, "set -x ANTHROPIC_AUTH_TOKEN 'sk-new\\'token\\\\123456'")
	assertContains(t, text, "set -x ANTHROPIC_BASE_URL 'https://gateway.example.com' # base comment")
	assertContains(t, text, "set -x ANTHROPIC_DEFAULT_HAIKU_MODEL (choose-haiku)")
	assertContains(t, text, "set -x ANTHROPIC_DEFAULT_HAIKU_MODEL 'claude-haiku'")
	assertIssue(t, result.Skipped, IssueDynamic, core.VarAnthropicDefaultHaikuModel, 4)

	second, _, err := UpsertProfile(output, SyntaxFish, values, ModeSetup)
	if err != nil {
		t.Fatalf("second UpsertProfile() error = %v", err)
	}
	if string(second) != text {
		t.Fatalf("UpsertProfile() should be idempotent\nfirst:\n%s\nsecond:\n%s", text, second)
	}
}

func TestUpsertFishUpdatesInheritedExportAssignments(t *testing.T) {
	input := []byte(strings.Join([]string{
		"set -x ANTHROPIC_MODEL 'claude-old'",
		"set ANTHROPIC_MODEL 'claude-stale'",
		"",
	}, "\n"))
	values := map[string]string{
		core.VarAnthropicModel: "claude-new",
	}

	output, result, err := UpsertProfile(input, SyntaxFish, values, ModeSetup)
	if err != nil {
		t.Fatalf("UpsertProfile() error = %v", err)
	}

	text := string(output)
	assertContains(t, text, "set -x ANTHROPIC_MODEL 'claude-new'")
	assertNotContains(t, text, "claude-stale")
	assertProfileValue(t, result.Profile, core.VarAnthropicModel, "claude-new", 2)
}

func TestLegacyManagedBlocksAreNotSpecial(t *testing.T) {
	input, err := os.ReadFile("testdata/legacy_block.sh")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	values := map[string]string{
		core.VarAnthropicBaseURL: "https://gateway.example.com",
	}

	output, result, err := UpsertProfile(input, SyntaxPOSIX, values, ModeSetup)
	if err != nil {
		t.Fatalf("UpsertProfile() error = %v", err)
	}
	text := string(output)
	assertContains(t, text, "# BEGIN LLMGATE")
	assertContains(t, text, "# export ANTHROPIC_BASE_URL='https://old.example.com'")
	assertContains(t, text, "# END LLMGATE")
	assertContains(t, text, "export ANTHROPIC_BASE_URL='https://gateway.example.com'")
	assertProfileValue(t, result.Profile, core.VarAnthropicBaseURL, "https://gateway.example.com", 4)
	if len(result.Profile.Issues) != 0 {
		t.Fatalf("Issues = %#v, want none", result.Profile.Issues)
	}
}

func assertProfileValue(t *testing.T, profile Profile, name, value string, line int) {
	t.Helper()
	got, ok := profile.Values[name]
	if !ok {
		t.Fatalf("Values[%s] missing", name)
	}
	if got.Value != value || got.Line != line {
		t.Fatalf("Values[%s] = {%q line %d}, want {%q line %d}", name, got.Value, got.Line, value, line)
	}
}

func assertProfileValueMissing(t *testing.T, profile Profile, name string) {
	t.Helper()
	if got, ok := profile.Values[name]; ok {
		t.Fatalf("Values[%s] = %#v, want missing", name, got)
	}
}

func assertIssue(t *testing.T, issues []Issue, kind IssueKind, name string, line int) {
	t.Helper()
	for _, issue := range issues {
		if issue.Kind == kind && issue.Name == name && (line == 0 || issue.Line == line) {
			return
		}
	}
	t.Fatalf("issue %s for %s line %d not found in %#v", kind, name, line, issues)
}

func assertContains(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Fatalf("output missing %q:\n%s", want, text)
	}
}

func assertNotContains(t *testing.T, text, notWant string) {
	t.Helper()
	if strings.Contains(text, notWant) {
		t.Fatalf("output unexpectedly contains %q:\n%s", notWant, text)
	}
}
