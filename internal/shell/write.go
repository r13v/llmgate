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
	skipped := make([]Issue, 0)

	for i, line := range lines {
		assignments := parser(line.Text, i+1)
		assignmentsByLine[i] = assignments
		for _, assignment := range assignments {
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
			updated := assignment
			updated.Value = value
			rewritten = &updated
			updatedNames[assignment.Name] = true
			break
		}
		if rewritten != nil {
			lines[i].Text = formatAssignment(syntax, rewritten.Name, rewritten.Value) + rewritten.Comment
		}
	}

	if mode == ModeSetup {
		appendMissing(&lines, syntax, values, updatedNames)
	}

	output := renderLines(lines)
	profile, err := ParseProfile(output, syntax)
	if err != nil {
		return nil, WriteResult{}, err
	}
	result := WriteResult{
		Profile: profile,
		Changed: string(output) != string(data),
		Skipped: skipped,
	}
	return output, result, nil
}

func UpsertPOSIX(data []byte, values map[string]string, mode WriteMode) ([]byte, WriteResult, error) {
	return UpsertProfile(data, SyntaxPOSIX, values, mode)
}

func UpsertFish(data []byte, values map[string]string, mode WriteMode) ([]byte, WriteResult, error) {
	return UpsertProfile(data, SyntaxFish, values, mode)
}

func appendMissing(lines *[]parsedLine, syntax Syntax, values map[string]string, updatedNames map[string]bool) {
	for _, name := range orderedManagedNames(values) {
		if updatedNames[name] {
			continue
		}
		*lines = append(*lines, parsedLine{
			Text: formatAssignment(syntax, name, values[name]),
			EOL:  "\n",
		})
	}
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
