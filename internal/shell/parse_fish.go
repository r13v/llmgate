package shell

import (
	"strings"

	"github.com/r13v/llmgate/internal/core"
)

func parseFishLine(line string, lineNumber int) []Assignment {
	code, comment := splitInlineComment(line)
	code = strings.TrimSpace(code)
	if code == "" || strings.HasPrefix(code, "#") {
		return nil
	}

	words, ok := lexShellWords(code, SyntaxFish)
	if !ok || len(words) == 0 {
		return complexAssignments(line, lineNumber, comment, managedNamesInText(code))
	}

	names := managedNamesInWords(words)
	if len(names) == 0 {
		return nil
	}
	if words[0].Text != "set" {
		return complexAssignments(line, lineNumber, comment, names)
	}

	nameIndex, exports := fishNameIndex(words)
	if nameIndex < 0 || nameIndex >= len(words) {
		return complexAssignments(line, lineNumber, comment, names)
	}
	if !exports {
		return complexAssignments(line, lineNumber, comment, names)
	}

	name := words[nameIndex].Text
	if !core.IsManaged(name) {
		return nil
	}

	valueWords := words[nameIndex+1:]
	if len(valueWords) != 1 {
		return exportedComplexAssignments(line, lineNumber, comment, []string{name})
	}
	if valueWords[0].Dynamic {
		return []Assignment{exportedDynamicAssignment(name, lineNumber, comment)}
	}
	return []Assignment{exportedSimpleAssignment(name, valueWords[0].Text, lineNumber, comment)}
}

func fishNameIndex(words []shellWord) (int, bool) {
	exports := false
	for i := 1; i < len(words); i++ {
		text := words[i].Text
		if !strings.HasPrefix(text, "-") {
			return i, exports
		}
		if text == "--" {
			if i+1 < len(words) {
				return i + 1, exports
			}
			return -1, exports
		}
		if fishSetOptionExports(text) {
			exports = true
		}
		if fishSetOptionUnexports(text) {
			exports = false
		}
	}
	return -1, exports
}

func fishSetOptionExports(option string) bool {
	if option == "--export" {
		return true
	}
	if strings.HasPrefix(option, "--") {
		return false
	}
	return strings.Contains(option[1:], "x")
}

func fishSetOptionUnexports(option string) bool {
	if option == "--unexport" {
		return true
	}
	if strings.HasPrefix(option, "--") {
		return false
	}
	return strings.Contains(option[1:], "u")
}
