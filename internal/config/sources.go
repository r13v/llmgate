package config

import (
	"sort"

	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/shell"
)

type SourceIssueKind string

const (
	SourceIssueMalformed SourceIssueKind = "malformed"
	SourceIssueReadError SourceIssueKind = "read_error"
)

type ConflictKind string

const (
	ConflictPersistedValue ConflictKind = "persisted_value"
	ConflictShellDuplicate ConflictKind = "shell_duplicate"
	ConflictShellDynamic   ConflictKind = "shell_dynamic"
	ConflictShellComplex   ConflictKind = "shell_complex"
)

type DifferenceKind string

const (
	DifferenceCurrentOnly     DifferenceKind = "current_only"
	DifferencePersistedOnly   DifferenceKind = "persisted_only"
	DifferenceCurrentDiffers  DifferenceKind = "current_differs"
	DifferenceProjectOverride DifferenceKind = "project_override"
	DifferenceIDEDrift        DifferenceKind = "ide_drift"
)

type Source struct {
	Label         core.SourceLabel
	Values        map[string]core.ConfigValue
	SelectedModel *core.ConfigValue
	ShellIssues   []shell.Issue
}

type SourceIssue struct {
	Kind    SourceIssueKind
	Status  core.DiagnosticStatus
	Source  core.SourceLabel
	Summary string
}

type ConflictIssue struct {
	Kind      ConflictKind
	Name      string
	Status    core.DiagnosticStatus
	Effective core.ResolvedValue
	Values    []core.ConfigValue
	Issue     shell.Issue
	Summary   string
}

type RuntimeDifference struct {
	Kind      DifferenceKind
	Name      string
	Current   *core.ResolvedValue
	Persisted *core.ResolvedValue
}

type SideContextDifference struct {
	Kind            DifferenceKind
	Name            string
	Context         core.SourceLabel
	ContextValue    core.ConfigValue
	Global          *core.ResolvedValue
	ComparedAgainst string
}

func source(label core.SourceLabel, values map[string]string) Source {
	return Source{
		Label:  label,
		Values: configValues(label, values),
	}
}

func configValues(label core.SourceLabel, values map[string]string) map[string]core.ConfigValue {
	out := make(map[string]core.ConfigValue, len(values))
	for name, value := range values {
		if !core.IsManaged(name) {
			continue
		}
		out[name] = core.ConfigValue{
			Name:   name,
			Value:  value,
			Source: label,
			Secret: core.IsSecret(name),
		}
	}
	return out
}

func selectedModelValue(label core.SourceLabel, value string) core.ConfigValue {
	label.Detail = "selected model"
	return core.ConfigValue{
		Name:   core.VarAnthropicModel,
		Value:  value,
		Source: label,
		Secret: false,
	}
}

func sourceIssue(kind SourceIssueKind, status core.DiagnosticStatus, label core.SourceLabel, summary string) SourceIssue {
	return SourceIssue{
		Kind:    kind,
		Status:  status,
		Source:  label,
		Summary: summary,
	}
}

func orderedManagedNamesFromMaps(maps ...map[string]core.ResolvedValue) []string {
	seen := make(map[string]bool)
	names := make([]string, 0)
	for _, name := range core.AllManagedNames() {
		for _, values := range maps {
			if _, ok := values[name]; ok {
				names = append(names, name)
				seen[name] = true
				break
			}
		}
	}

	var extras []string
	for _, values := range maps {
		for name := range values {
			if seen[name] {
				continue
			}
			extras = append(extras, name)
			seen[name] = true
		}
	}
	sort.Strings(extras)
	return append(names, extras...)
}

func resolvedPtr(value core.ResolvedValue) *core.ResolvedValue {
	copied := value
	return &copied
}
