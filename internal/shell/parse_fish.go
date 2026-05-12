package shell

import (
	"strings"

	"github.com/r13v/llmgate/internal/core"
)

type fishExportMode int

const (
	fishExportManual fishExportMode = iota
	fishExportInherit
	fishExportOn
	fishExportOff
	fishErase
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

	nameIndex, exportMode := fishNameIndex(words)
	if nameIndex < 0 || nameIndex >= len(words) {
		return complexAssignments(line, lineNumber, comment, names)
	}
	if exportMode == fishExportManual {
		return complexAssignments(line, lineNumber, comment, names)
	}

	name := words[nameIndex].Text
	if !core.IsManaged(name) {
		return nil
	}

	valueWords := words[nameIndex+1:]
	if exportMode == fishErase {
		if len(valueWords) != 0 {
			return complexAssignments(line, lineNumber, comment, names)
		}
		return unexportingStateAssignments(lineNumber, comment, []string{name})
	}
	if len(valueWords) != 1 {
		if exportMode == fishExportOn {
			return exportedComplexAssignments(line, lineNumber, comment, []string{name})
		}
		return complexAssignments(line, lineNumber, comment, []string{name})
	}
	if valueWords[0].Dynamic {
		return []Assignment{fishDynamicAssignment(name, lineNumber, comment, exportMode)}
	}
	return []Assignment{fishSimpleAssignment(name, valueWords[0].Text, lineNumber, comment, exportMode)}
}

func fishNameIndex(words []shellWord) (int, fishExportMode) {
	mode := fishExportInherit
	sawOption := false
	for i := 1; i < len(words); i++ {
		text := words[i].Text
		if !strings.HasPrefix(text, "-") {
			if sawOption && mode == fishExportInherit {
				return i, fishExportManual
			}
			return i, mode
		}
		if text == "--" {
			if i+1 < len(words) {
				return i + 1, mode
			}
			return -1, mode
		}
		sawOption = true
		if fishSetOptionExports(text) {
			mode = fishExportOn
		}
		if fishSetOptionUnexports(text) {
			mode = fishExportOff
		}
		if fishSetOptionErases(text) {
			mode = fishErase
		}
	}
	return -1, mode
}

func fishSimpleAssignment(name, value string, line int, comment string, mode fishExportMode) Assignment {
	switch mode {
	case fishExportOn:
		return exportedSimpleAssignment(name, value, line, comment)
	case fishExportOff:
		return unexportingSimpleAssignment(name, value, line, comment)
	default:
		return inheritingSimpleAssignment(name, value, line, comment)
	}
}

func fishDynamicAssignment(name string, line int, comment string, mode fishExportMode) Assignment {
	switch mode {
	case fishExportOn:
		return exportedDynamicAssignment(name, line, comment)
	case fishExportOff:
		return unexportingDynamicAssignment(name, line, comment)
	default:
		return inheritingDynamicAssignment(name, line, comment)
	}
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

func fishSetOptionErases(option string) bool {
	if option == "--erase" {
		return true
	}
	if strings.HasPrefix(option, "--") {
		return false
	}
	return strings.Contains(option[1:], "e")
}
