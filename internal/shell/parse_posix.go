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
		names := managedNamesInWords(words[1:])
		if posixDeclareExports(words[1:]) {
			return exportedComplexAssignments(line, lineNumber, comment, names)
		}
		return complexAssignments(line, lineNumber, comment, names)
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
	exports := posixExportMarksExport(words)
	if len(words) != 1 {
		if exports {
			return exportedComplexAssignments(line, lineNumber, comment, names)
		}
		return complexAssignments(line, lineNumber, comment, names)
	}

	name, value, ok := strings.Cut(words[0].Text, "=")
	if !ok {
		if exports {
			return exportedComplexAssignments(line, lineNumber, comment, names)
		}
		return complexAssignments(line, lineNumber, comment, names)
	}
	if !core.IsManaged(name) {
		return nil
	}
	if words[0].Dynamic {
		return []Assignment{exportedDynamicAssignment(name, lineNumber, comment)}
	}
	return []Assignment{exportedSimpleAssignment(name, value, lineNumber, comment)}
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
		return []Assignment{inheritingDynamicAssignment(name, lineNumber, comment)}
	}
	return []Assignment{inheritingSimpleAssignment(name, value, lineNumber, comment)}
}

func posixExportMarksExport(words []shellWord) bool {
	for _, word := range words {
		text := word.Text
		if text == "--" {
			return true
		}
		if !strings.HasPrefix(text, "-") || text == "-" {
			return true
		}
		if strings.Contains(text[1:], "n") {
			return false
		}
	}
	return true
}

func posixDeclareExports(words []shellWord) bool {
	for _, word := range words {
		text := word.Text
		if text == "--" {
			return false
		}
		if strings.HasPrefix(text, "-") && strings.Contains(text[1:], "x") {
			return true
		}
		if !strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "+") {
			return false
		}
	}
	return false
}
