package shell

import (
	"fmt"
	"strings"

	"github.com/r13v/llmgate/internal/core"
)

type WriteResult struct {
	Profile Profile
	Changed bool
	Skipped []Issue
}

func UpsertProfile(data []byte, syntax Syntax, values map[string]string, mode WriteMode) ([]byte, WriteResult, error) {
	parser, err := parserForSyntax(syntax)
	if err != nil {
		return nil, WriteResult{}, err
	}

	lines := splitLines(string(data))
	assignmentsByLine := make(map[int][]Assignment)
	updatedNames := make(map[string]bool)
	lastAssignments := make(map[string]Assignment)
	skipped := make([]Issue, 0)
	changed := false

	for i, line := range lines {
		assignments := parser(line.Text, i+1)
		assignmentsByLine[i] = assignments
		for _, assignment := range assignments {
			if _, ok := values[assignment.Name]; ok {
				lastAssignments[assignment.Name] = assignment
			}
			if assignment.Kind == AssignmentSimple {
				continue
			}
			if _, ok := values[assignment.Name]; ok {
				skipped = append(skipped, Issue{
					Kind:    issueKindForAssignment(assignment.Kind),
					Name:    assignment.Name,
					Line:    assignment.Line,
					Summary: fmt.Sprintf("%s on line %d requires manual review and was not modified", assignment.Name, assignment.Line),
				})
			}
		}
	}

	for i := range lines {
		assignments := assignmentsByLine[i]
		if len(assignments) == 0 {
			continue
		}

		var rewritten *Assignment
		for j := range assignments {
			assignment := assignments[j]
			value, ok := values[assignment.Name]
			if !ok || assignment.Kind != AssignmentSimple {
				continue
			}
			updatedNames[assignment.Name] = true
			if assignment.Value == value {
				continue
			}
			updated := assignment
			updated.Value = value
			rewritten = &updated
			break
		}
		if rewritten != nil {
			text := formatAssignment(syntax, rewritten.Name, rewritten.Value) + rewritten.Comment
			if lines[i].Text != text {
				lines[i].Text = text
				changed = true
			}
		}
	}

	if mode == ModeSetup {
		changed = appendMissing(&lines, syntax, values, effectiveUpdatedNames(updatedNames, lastAssignments)) || changed
	}

	output := append([]byte(nil), data...)
	if changed {
		output = renderLines(lines)
	}
	profile, err := ParseProfile(output, syntax)
	if err != nil {
		return nil, WriteResult{}, err
	}
	result := WriteResult{
		Profile: profile,
		Changed: changed,
		Skipped: skipped,
	}
	return output, result, nil
}

func appendMissing(lines *[]parsedLine, syntax Syntax, values map[string]string, updatedNames map[string]bool) bool {
	changed := false
	for _, name := range orderedManagedNames(values) {
		if updatedNames[name] {
			continue
		}
		*lines = append(*lines, parsedLine{
			Text: formatAssignment(syntax, name, values[name]),
			EOL:  "\n",
		})
		changed = true
	}
	return changed
}

func effectiveUpdatedNames(updatedNames map[string]bool, lastAssignments map[string]Assignment) map[string]bool {
	effective := make(map[string]bool, len(updatedNames))
	for name := range updatedNames {
		last, ok := lastAssignments[name]
		if ok && last.Kind != AssignmentSimple {
			continue
		}
		effective[name] = true
	}
	return effective
}

func formatAssignment(syntax Syntax, name, value string) string {
	switch syntax {
	case SyntaxFish:
		return "set -x " + name + " " + quoteFish(value)
	default:
		return "export " + name + "=" + quotePOSIX(value)
	}
}

func quotePOSIX(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func quoteFish(value string) string {
	if value == "" {
		return "''"
	}
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "'", "\\'")
	return "'" + value + "'"
}

func issueKindForAssignment(kind AssignmentKind) IssueKind {
	switch kind {
	case AssignmentDynamic:
		return IssueDynamic
	case AssignmentComplex:
		return IssueComplex
	default:
		return IssueComplex
	}
}

func ManualSetupLines(syntax Syntax, values map[string]string) ([]string, error) {
	if _, err := parserForSyntax(syntax); err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(values))
	for _, name := range orderedManagedNames(values) {
		if !core.IsManaged(name) {
			continue
		}
		lines = append(lines, formatAssignment(syntax, name, values[name]))
	}
	return lines, nil
}
