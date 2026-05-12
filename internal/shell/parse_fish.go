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

	nameIndex := fishNameIndex(words)
	if nameIndex < 0 || nameIndex >= len(words) {
		return complexAssignments(line, lineNumber, comment, names)
	}

	name := words[nameIndex].Text
	if !core.IsManaged(name) {
		return nil
	}

	valueWords := words[nameIndex+1:]
	if len(valueWords) != 1 {
		return complexAssignments(line, lineNumber, comment, []string{name})
	}
	if valueWords[0].Dynamic {
		return []Assignment{dynamicAssignment(name, lineNumber, comment)}
	}
	return []Assignment{simpleAssignment(name, valueWords[0].Text, lineNumber, comment)}
}

func fishNameIndex(words []shellWord) int {
	for i := 1; i < len(words); i++ {
		text := words[i].Text
		if !strings.HasPrefix(text, "-") {
			return i
		}
		if text == "--" {
			if i+1 < len(words) {
				return i + 1
			}
			return -1
		}
	}
	return -1
}
