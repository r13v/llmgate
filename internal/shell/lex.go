package shell

import (
	"strings"
	"unicode"

	"github.com/r13v/llmgate/internal/core"
)

type shellWord struct {
	Text    string
	Dynamic bool
}

func splitInlineComment(line string) (string, string) {
	var quote rune
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if quote == 0 && r == '\\' {
			escaped = true
			continue
		}
		switch r {
		case '\'', '"':
			switch quote {
			case 0:
				quote = r
			case r:
				quote = 0
			}
		case '#':
			if quote == 0 && (i == 0 || unicode.IsSpace(rune(line[i-1]))) {
				code := strings.TrimRight(line[:i], " \t")
				gap := line[len(code):i]
				return code, gap + line[i:]
			}
		}
	}
	return line, ""
}

func lexShellWords(input string, syntax Syntax) ([]shellWord, bool) {
	var words []shellWord
	for i := 0; i < len(input); {
		for i < len(input) && isShellSpace(input[i]) {
			i++
		}
		if i >= len(input) {
			break
		}

		word, next, ok := readShellWord(input, i, syntax)
		if !ok {
			return nil, false
		}
		words = append(words, word)
		i = next
	}
	return words, true
}

func readShellWord(input string, start int, syntax Syntax) (shellWord, int, bool) {
	var builder strings.Builder
	var quote byte
	dynamic := false

	for i := start; i < len(input); i++ {
		ch := input[i]
		if quote == 0 && isShellSpace(ch) {
			return shellWord{Text: builder.String(), Dynamic: dynamic}, i, true
		}

		switch quote {
		case 0:
			switch ch {
			case '\'', '"':
				quote = ch
			case '\\':
				if i+1 >= len(input) {
					builder.WriteByte(ch)
					continue
				}
				i++
				builder.WriteByte(input[i])
			default:
				if ch == '$' || ch == '`' || (syntax == SyntaxFish && ch == '(') {
					dynamic = true
				}
				builder.WriteByte(ch)
			}
		case '\'':
			if ch == '\'' {
				quote = 0
				continue
			}
			if syntax == SyntaxFish && ch == '\\' && i+1 < len(input) && (input[i+1] == '\'' || input[i+1] == '\\') {
				i++
				builder.WriteByte(input[i])
				continue
			}
			builder.WriteByte(ch)
		case '"':
			if ch == '"' {
				quote = 0
				continue
			}
			if ch == '\\' && i+1 < len(input) {
				i++
				builder.WriteByte(input[i])
				continue
			}
			if ch == '$' || ch == '`' {
				dynamic = true
			}
			builder.WriteByte(ch)
		}
	}

	if quote != 0 {
		return shellWord{}, 0, false
	}
	return shellWord{Text: builder.String(), Dynamic: dynamic}, len(input), true
}

func isShellSpace(ch byte) bool {
	return ch == ' ' || ch == '\t'
}

func simpleAssignment(name, value string, line int, comment string) Assignment {
	return Assignment{
		Name:    name,
		Value:   value,
		Line:    line,
		Kind:    AssignmentSimple,
		Comment: comment,
	}
}

func exportedSimpleAssignment(name, value string, line int, comment string) Assignment {
	assignment := simpleAssignment(name, value, line, comment)
	assignment.Exports = true
	return assignment
}

func inheritingSimpleAssignment(name, value string, line int, comment string) Assignment {
	assignment := simpleAssignment(name, value, line, comment)
	assignment.InheritsExport = true
	return assignment
}

func dynamicAssignment(name string, line int, comment string) Assignment {
	return Assignment{
		Name:    name,
		Line:    line,
		Kind:    AssignmentDynamic,
		Comment: comment,
	}
}

func exportedDynamicAssignment(name string, line int, comment string) Assignment {
	assignment := dynamicAssignment(name, line, comment)
	assignment.Exports = true
	return assignment
}

func inheritingDynamicAssignment(name string, line int, comment string) Assignment {
	assignment := dynamicAssignment(name, line, comment)
	assignment.InheritsExport = true
	return assignment
}

func complexAssignments(line string, lineNumber int, comment string, names []string) []Assignment {
	return complexAssignmentsWithExports(line, lineNumber, comment, names, false)
}

func exportedComplexAssignments(line string, lineNumber int, comment string, names []string) []Assignment {
	return complexAssignmentsWithExports(line, lineNumber, comment, names, true)
}

func complexAssignmentsWithExports(line string, lineNumber int, comment string, names []string, exports bool) []Assignment {
	names = uniqueStrings(names)
	assignments := make([]Assignment, 0, len(names))
	for _, name := range names {
		if !core.IsManaged(name) {
			continue
		}
		assignments = append(assignments, Assignment{
			Name:    name,
			Line:    lineNumber,
			Kind:    AssignmentComplex,
			Comment: comment,
			Exports: exports,
		})
	}
	return assignments
}

func managedNamesInWords(words []shellWord) []string {
	names := make([]string, 0, len(words))
	for _, word := range words {
		if core.IsManaged(word.Text) {
			names = append(names, word.Text)
			continue
		}
		name, _, ok := strings.Cut(word.Text, "=")
		if ok && core.IsManaged(name) {
			names = append(names, name)
		}
	}
	return uniqueStrings(names)
}

func managedNamesInText(text string) []string {
	names := make([]string, 0)
	for _, name := range core.AllManagedNames() {
		if strings.Contains(text, name) {
			names = append(names, name)
		}
	}
	return names
}
