package shell

import (
	"strings"

	"github.com/r13v/llmgate/internal/core"
)

func parsePOSIXLine(line string, lineNumber int) []Assignment {
	code, comment := splitInlineComment(line)
	code = strings.TrimSpace(code)
	if code == "" || strings.HasPrefix(code, "#") {
		return nil
	}

	words, ok := lexShellWords(code, SyntaxPOSIX)
	if !ok || len(words) == 0 {
		return complexAssignments(line, lineNumber, comment, managedNamesInText(code))
	}

	switch words[0].Text {
	case "declare", "typeset":
		return complexAssignments(line, lineNumber, comment, managedNamesInWords(words[1:]))
	case "export":
		return parsePOSIXExport(line, lineNumber, comment, words[1:])
	default:
		return parsePOSIXBareAssignment(line, lineNumber, comment, words)
	}
}

func parsePOSIXExport(line string, lineNumber int, comment string, words []shellWord) []Assignment {
	names := managedNamesInWords(words)
	if len(names) == 0 {
		return nil
	}
	if len(words) != 1 {
		return complexAssignments(line, lineNumber, comment, names)
	}

	name, value, ok := strings.Cut(words[0].Text, "=")
	if !ok {
		return complexAssignments(line, lineNumber, comment, names)
	}
	if !core.IsManaged(name) {
		return nil
	}
	if words[0].Dynamic {
		return []Assignment{dynamicAssignment(name, lineNumber, comment)}
	}
	return []Assignment{simpleAssignment(name, value, lineNumber, comment)}
}

func parsePOSIXBareAssignment(line string, lineNumber int, comment string, words []shellWord) []Assignment {
	names := managedNamesInWords(words)
	if len(names) == 0 {
		return nil
	}
	if len(words) != 1 {
		return complexAssignments(line, lineNumber, comment, names)
	}

	name, value, ok := strings.Cut(words[0].Text, "=")
	if !ok || !core.IsManaged(name) {
		return complexAssignments(line, lineNumber, comment, names)
	}
	if words[0].Dynamic {
		return []Assignment{dynamicAssignment(name, lineNumber, comment)}
	}
	return []Assignment{simpleAssignment(name, value, lineNumber, comment)}
}
