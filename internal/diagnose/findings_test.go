package diagnose

import (
	"reflect"
	"testing"

	"github.com/r13v/llmgate/internal/core"
)

func TestOrderedFindingsSortsBySeverity(t *testing.T) {
	findings := []core.DiagnosticFinding{
		{ID: "ok", Status: core.StatusOK},
		{ID: "warn", Status: core.StatusWARN},
		{ID: "skip", Status: core.StatusSKIP},
		{ID: "fail", Status: core.StatusFAIL},
	}

	ordered := OrderedFindings(findings)

	got := findingIDs(ordered)
	want := []string{"fail", "warn", "skip", "ok"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OrderedFindings IDs = %#v, want %#v", got, want)
	}
	if got := findingIDs(findings); !reflect.DeepEqual(got, []string{"ok", "warn", "skip", "fail"}) {
		t.Fatalf("OrderedFindings mutated input IDs = %#v", got)
	}
}

func TestOrderedFindingsPreservesInputOrderWithinSeverity(t *testing.T) {
	findings := []core.DiagnosticFinding{
		{ID: "warn-1", Status: core.StatusWARN},
		{ID: "fail-1", Status: core.StatusFAIL},
		{ID: "warn-2", Status: core.StatusWARN},
		{ID: "fail-2", Status: core.StatusFAIL},
		{ID: "warn-3", Status: core.StatusWARN},
	}

	got := findingIDs(OrderedFindings(findings))
	want := []string{"fail-1", "fail-2", "warn-1", "warn-2", "warn-3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OrderedFindings IDs = %#v, want %#v", got, want)
	}
}

func TestCoveredCheckIDsDerivesUniqueNonEmptyRelatedChecks(t *testing.T) {
	findings := []core.DiagnosticFinding{
		{ID: "gateway", RelatedChecks: []string{"gateway.validation", "config-conflict.01.ANTHROPIC_AUTH_TOKEN"}},
		{ID: "config", RelatedChecks: []string{"config-conflict.01.ANTHROPIC_AUTH_TOKEN", ""}},
		{ID: "ide"},
	}

	got := CoveredCheckIDs(findings)
	want := map[string]bool{
		"gateway.validation":                      true,
		"config-conflict.01.ANTHROPIC_AUTH_TOKEN": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CoveredCheckIDs() = %#v, want %#v", got, want)
	}
}

func TestFindingHelpersHandleEmptyAndDefaultFindings(t *testing.T) {
	ordered := OrderedFindings(nil)
	if len(ordered) != 0 {
		t.Fatalf("OrderedFindings(nil) length = %d, want 0", len(ordered))
	}

	covered := CoveredCheckIDs(nil)
	if len(covered) != 0 {
		t.Fatalf("CoveredCheckIDs(nil) length = %d, want 0", len(covered))
	}

	findings := []core.DiagnosticFinding{
		{ID: "default"},
		{ID: "warn", Status: core.StatusWARN},
	}
	got := findingIDs(OrderedFindings(findings))
	want := []string{"warn", "default"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OrderedFindings default status IDs = %#v, want %#v", got, want)
	}
}

func findingIDs(findings []core.DiagnosticFinding) []string {
	ids := make([]string, 0, len(findings))
	for _, finding := range findings {
		ids = append(ids, finding.ID)
	}
	return ids
}
