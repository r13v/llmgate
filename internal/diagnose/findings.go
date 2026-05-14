package diagnose

import (
	"sort"

	"github.com/r13v/llmgate/internal/core"
)

// OrderedFindings returns a copy of findings sorted from most to least severe.
func OrderedFindings(findings []core.DiagnosticFinding) []core.DiagnosticFinding {
	ordered := append([]core.DiagnosticFinding(nil), findings...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return findingSeverity(ordered[i].Status) > findingSeverity(ordered[j].Status)
	})
	return ordered
}

// CoveredCheckIDs returns the diagnostic check IDs referenced by findings.
func CoveredCheckIDs(findings []core.DiagnosticFinding) map[string]bool {
	covered := make(map[string]bool)
	for _, finding := range findings {
		for _, checkID := range finding.RelatedChecks {
			if checkID == "" {
				continue
			}
			covered[checkID] = true
		}
	}
	return covered
}

func findingSeverity(status core.DiagnosticStatus) int {
	switch status {
	case core.StatusFAIL:
		return 4
	case core.StatusWARN:
		return 3
	case core.StatusSKIP:
		return 2
	case core.StatusOK:
		return 1
	default:
		return 0
	}
}
