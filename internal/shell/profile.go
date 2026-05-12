package shell

import (
	"fmt"
	"sort"
	"strings"

	"github.com/r13v/llmgate/internal/core"
)

type Syntax string

const (
	SyntaxPOSIX Syntax = "posix"
	SyntaxFish  Syntax = "fish"
)

type WriteMode int

const (
	ModeSetup WriteMode = iota
	ModeRepair
)

type AssignmentKind string

const (
	AssignmentSimple  AssignmentKind = "simple"
	AssignmentDynamic AssignmentKind = "dynamic"
	AssignmentComplex AssignmentKind = "complex"
)

type IssueKind string

const (
	IssueDuplicate IssueKind = "duplicate"
	IssueDynamic   IssueKind = "dynamic"
	IssueComplex   IssueKind = "complex"
)

type Value struct {
	Name  string
	Value string
	Line  int
}

type Assignment struct {
	Name           string
	Value          string
	Line           int
	Kind           AssignmentKind
	Comment        string
	Exports        bool
	InheritsExport bool
}

type Issue struct {
	Kind    IssueKind
	Name    string
	Line    int
	Lines   []int
	Summary string
}

type Profile struct {
	Syntax      Syntax
	Values      map[string]Value
	Assignments []Assignment
	Issues      []Issue
	Duplicates  []Issue
	Manual      []Issue
}

type parsedLine struct {
	Text string
	EOL  string
}

type parseLineFunc func(string, int) []Assignment

func ParseProfile(data []byte, syntax Syntax) (Profile, error) {
	parser, err := parserForSyntax(syntax)
	if err != nil {
		return Profile{}, err
	}
	lines := splitLines(string(data))
	profile := Profile{
		Syntax: syntax,
		Values: make(map[string]Value),
	}

	for i, line := range lines {
		assignments := parser(line.Text, i+1)
		profile.Assignments = append(profile.Assignments, assignments...)
	}

	profile.finish()
	return profile, nil
}

func (p *Profile) finish() {
	simpleByName := make(map[string][]Assignment)
	lastByName := make(map[string]Assignment)
	exportedByName := make(map[string]bool)

	for _, assignment := range p.Assignments {
		effective := assignment.Exports || (assignment.InheritsExport && exportedByName[assignment.Name])
		switch assignment.Kind {
		case AssignmentSimple:
			if effective {
				simpleByName[assignment.Name] = append(simpleByName[assignment.Name], assignment)
				lastByName[assignment.Name] = assignment
			}
		case AssignmentDynamic:
			if effective {
				lastByName[assignment.Name] = assignment
			}
			p.Manual = append(p.Manual, Issue{
				Kind:    IssueDynamic,
				Name:    assignment.Name,
				Line:    assignment.Line,
				Summary: fmt.Sprintf("%s uses a dynamic shell assignment on line %d", assignment.Name, assignment.Line),
			})
		case AssignmentComplex:
			if effective {
				lastByName[assignment.Name] = assignment
			}
			p.Manual = append(p.Manual, Issue{
				Kind:    IssueComplex,
				Name:    assignment.Name,
				Line:    assignment.Line,
				Summary: fmt.Sprintf("%s uses a complex shell assignment on line %d", assignment.Name, assignment.Line),
			})
		}
		if assignment.Exports {
			exportedByName[assignment.Name] = true
		}
	}

	for name, assignments := range simpleByName {
		if len(assignments) > 1 {
			lines := make([]int, 0, len(assignments))
			for _, assignment := range assignments {
				lines = append(lines, assignment.Line)
			}
			p.Duplicates = append(p.Duplicates, Issue{
				Kind:    IssueDuplicate,
				Name:    name,
				Lines:   lines,
				Summary: fmt.Sprintf("%s has multiple active simple shell assignments on lines %s", name, joinInts(lines)),
			})
		}
	}

	for name, assignment := range lastByName {
		if assignment.Kind == AssignmentSimple {
			p.Values[name] = Value{Name: name, Value: assignment.Value, Line: assignment.Line}
		}
	}

	sortIssues(p.Duplicates)
	sortIssues(p.Manual)
	p.Issues = append(p.Issues, p.Duplicates...)
	p.Issues = append(p.Issues, p.Manual...)
}

func parserForSyntax(syntax Syntax) (parseLineFunc, error) {
	switch syntax {
	case SyntaxPOSIX:
		return parsePOSIXLine, nil
	case SyntaxFish:
		return parseFishLine, nil
	default:
		return nil, fmt.Errorf("unsupported shell profile syntax %q", syntax)
	}
}

func splitLines(input string) []parsedLine {
	if input == "" {
		return nil
	}

	lines := make([]parsedLine, 0, strings.Count(input, "\n")+1)
	for len(input) > 0 {
		line := parsedLine{}
		next := strings.IndexByte(input, '\n')
		if next < 0 {
			line.Text = strings.TrimSuffix(input, "\r")
			lines = append(lines, line)
			break
		}

		line.Text = input[:next]
		line.EOL = "\n"
		if strings.HasSuffix(line.Text, "\r") {
			line.Text = strings.TrimSuffix(line.Text, "\r")
			line.EOL = "\r\n"
		}
		lines = append(lines, line)
		input = input[next+1:]
	}
	return lines
}

func renderLines(lines []parsedLine) []byte {
	if len(lines) == 0 {
		return nil
	}

	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(line.Text)
		if line.EOL != "" {
			builder.WriteString(line.EOL)
		}
	}
	if !strings.HasSuffix(builder.String(), "\n") {
		builder.WriteByte('\n')
	}
	return []byte(builder.String())
}

func orderedManagedNames(values map[string]string) []string {
	names := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, name := range core.AllManagedNames() {
		if _, ok := values[name]; ok {
			names = append(names, name)
			seen[name] = true
		}
	}

	var extras []string
	for name := range values {
		if seen[name] || !core.IsManaged(name) {
			continue
		}
		extras = append(extras, name)
	}
	sort.Strings(extras)
	names = append(names, extras...)
	return names
}

func sortIssues(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Name != issues[j].Name {
			return issues[i].Name < issues[j].Name
		}
		return issues[i].Line < issues[j].Line
	})
}

func joinInts(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf("%d", value))
	}
	return strings.Join(parts, ", ")
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}
