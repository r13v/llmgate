package config

import (
	"fmt"
	"sort"

	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/shell"
)

func collectConflicts(sources []Source, persisted core.ResolvedConfig) []ConflictIssue {
	conflicts := make([]ConflictIssue, 0)
	conflicts = append(conflicts, collectPersistedValueConflicts(sources, persisted)...)
	for _, src := range sources {
		if src.Label.Kind != core.SourceShellProfile {
			continue
		}
		for _, issue := range src.ShellIssues {
			conflicts = append(conflicts, shellConflict(issue))
		}
	}
	sortConflicts(conflicts)
	return conflicts
}

func collectPersistedValueConflicts(sources []Source, persisted core.ResolvedConfig) []ConflictIssue {
	var conflicts []ConflictIssue
	for _, name := range core.AllManagedNames() {
		values := make([]core.ConfigValue, 0, len(sources))
		seen := make(map[string]bool)
		for _, src := range sources {
			value, ok := src.Values[name]
			if !ok {
				continue
			}
			values = append(values, value)
			seen[value.Value] = true
		}
		if len(seen) < 2 {
			continue
		}
		effective, _ := persisted.Get(name)
		conflicts = append(conflicts, ConflictIssue{
			Kind:      ConflictPersistedValue,
			Name:      name,
			Status:    core.StatusWARN,
			Effective: effective,
			Values:    values,
			Summary:   fmt.Sprintf("%s differs across persisted sources", name),
		})
	}
	return conflicts
}

func shellConflict(issue shell.Issue) ConflictIssue {
	kind := ConflictShellComplex
	switch issue.Kind {
	case shell.IssueDuplicate:
		kind = ConflictShellDuplicate
	case shell.IssueDynamic:
		kind = ConflictShellDynamic
	case shell.IssueComplex:
		kind = ConflictShellComplex
	}
	return ConflictIssue{
		Kind:    kind,
		Name:    issue.Name,
		Status:  core.StatusWARN,
		Issue:   issue,
		Summary: issue.Summary,
	}
}

func sortConflicts(conflicts []ConflictIssue) {
	sort.SliceStable(conflicts, func(i, j int) bool {
		if conflicts[i].Name != conflicts[j].Name {
			return conflicts[i].Name < conflicts[j].Name
		}
		return conflicts[i].Kind < conflicts[j].Kind
	})
}
