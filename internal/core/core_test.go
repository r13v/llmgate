package core

import "testing"

func TestManagedValuesDefineRequiredAndBehaviorDefaults(t *testing.T) {
	if len(RequiredValues) != 6 {
		t.Fatalf("len(RequiredValues) = %d, want 6", len(RequiredValues))
	}
	if len(BehaviorPrivacyDefaults) != 8 {
		t.Fatalf("len(BehaviorPrivacyDefaults) = %d, want 8", len(BehaviorPrivacyDefaults))
	}

	for _, name := range []string{
		VarAnthropicAuthToken,
		VarAnthropicBaseURL,
		VarAnthropicModel,
		VarAnthropicDefaultHaikuModel,
		VarAnthropicDefaultSonnetModel,
		VarAnthropicDefaultOpusModel,
		VarClaudeCodeEnableTelemetry,
		VarClaudeCodeDisableNonessentialTraffic,
		VarOTELMetricsExporter,
		VarAnthropicDisableNonessentialTraffic,
		VarDisablePromptCachingHaiku,
		VarDisablePromptCachingSonnet,
		VarDisablePromptCachingOpus,
		VarClaudeCodeDisableExperimentalBetas,
	} {
		if !IsManaged(name) {
			t.Fatalf("%s is not managed", name)
		}
	}

	if !IsSecret(VarAnthropicAuthToken) {
		t.Fatalf("%s should be secret", VarAnthropicAuthToken)
	}
	if IsSecret(VarAnthropicBaseURL) {
		t.Fatalf("%s should not be secret", VarAnthropicBaseURL)
	}
}

func TestSetupValuesMapIncludesRequiredValuesAndDefaults(t *testing.T) {
	values := SetupValues{
		AuthToken:   "token",
		BaseURL:     "https://gateway.example.com",
		Model:       "claude-primary",
		HaikuModel:  "claude-haiku",
		SonnetModel: "claude-sonnet",
		OpusModel:   "claude-opus",
	}.Map()

	wants := map[string]string{
		VarAnthropicAuthToken:                   "token",
		VarAnthropicBaseURL:                     "https://gateway.example.com",
		VarAnthropicModel:                       "claude-primary",
		VarAnthropicDefaultHaikuModel:           "claude-haiku",
		VarAnthropicDefaultSonnetModel:          "claude-sonnet",
		VarAnthropicDefaultOpusModel:            "claude-opus",
		VarClaudeCodeEnableTelemetry:            "0",
		VarClaudeCodeDisableNonessentialTraffic: "1",
		VarOTELMetricsExporter:                  "otlp",
		VarAnthropicDisableNonessentialTraffic:  "1",
		VarDisablePromptCachingHaiku:            "1",
		VarDisablePromptCachingSonnet:           "1",
		VarDisablePromptCachingOpus:             "1",
		VarClaudeCodeDisableExperimentalBetas:   "1",
	}
	for name, want := range wants {
		if got := values[name]; got != want {
			t.Fatalf("values[%s] = %q, want %q", name, got, want)
		}
	}
}

func TestDiagnosticAggregationSeverity(t *testing.T) {
	tests := []struct {
		name     string
		statuses []DiagnosticStatus
		want     DiagnosticStatus
	}{
		{name: "empty is ok", want: StatusOK},
		{name: "skip beats ok", statuses: []DiagnosticStatus{StatusOK, StatusSKIP}, want: StatusSKIP},
		{name: "warn beats skip", statuses: []DiagnosticStatus{StatusSKIP, StatusWARN}, want: StatusWARN},
		{name: "fail beats warn", statuses: []DiagnosticStatus{StatusWARN, StatusFAIL, StatusOK}, want: StatusFAIL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AggregateStatus(tt.statuses...); got != tt.want {
				t.Fatalf("AggregateStatus() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDiagnosticSectionStatus(t *testing.T) {
	section := DiagnosticSection{
		ID:    "config",
		Title: "Config",
		Checks: []DiagnosticCheck{
			{ID: "present", Status: StatusOK},
			{ID: "stale", Status: StatusWARN},
			{ID: "skipped", Status: StatusSKIP},
		},
	}

	if got := section.Status(); got != StatusWARN {
		t.Fatalf("section.Status() = %s, want WARN", got)
	}
}

func TestDiagnosticFindingCarriesStructuredFields(t *testing.T) {
	finding := DiagnosticFinding{
		ID:            "gateway.auth",
		Status:        StatusFAIL,
		Title:         "Gateway: token rejected",
		Summary:       "The configured gateway token was rejected.",
		Evidence:      []string{"HTTP status: 401"},
		Remediation:   "Update the active token.",
		RelatedChecks: []string{"gateway.validation"},
	}

	if finding.ID != "gateway.auth" {
		t.Fatalf("finding.ID = %q, want gateway.auth", finding.ID)
	}
	if finding.Status != StatusFAIL {
		t.Fatalf("finding.Status = %s, want FAIL", finding.Status)
	}
	if len(finding.Evidence) != 1 || finding.Evidence[0] != "HTTP status: 401" {
		t.Fatalf("finding.Evidence = %#v, want HTTP status evidence", finding.Evidence)
	}
	if len(finding.RelatedChecks) != 1 || finding.RelatedChecks[0] != "gateway.validation" {
		t.Fatalf("finding.RelatedChecks = %#v, want gateway.validation", finding.RelatedChecks)
	}
}
