package config

import (
	"sort"

	"github.com/r13v/llmgate/internal/core"
)

type Resolution struct {
	Persisted        core.ResolvedConfig
	Current          core.ResolvedConfig
	SourceIssues     []SourceIssue
	Conflicts        []ConflictIssue
	Runtime          []RuntimeDifference
	ProjectOverrides []SideContextDifference
	IDEDrift         []SideContextDifference
}

func Resolve(read ReadResult) Resolution {
	resolution := Resolution{
		SourceIssues: append([]SourceIssue(nil), read.SourceIssues...),
	}

	persistedSources := filterSources(read.Sources, core.SourceClaudeUserSettings, core.SourceShellProfile, core.SourceWindowsUserEnv)
	currentSources := filterSources(read.Sources, core.SourceClaudeUserSettings, core.SourceCurrentEnv)

	resolution.Persisted = resolveConfig("persisted config for new sessions", persistedSources, false)
	resolution.Current = resolveConfig("current environment", currentSources, true)
	resolution.Conflicts = collectConflicts(persistedSources, resolution.Persisted)
	resolution.Runtime = compareCurrentAndPersisted(resolution.Current, resolution.Persisted)
	resolution.ProjectOverrides = compareSideContexts(
		filterSources(read.Sources, core.SourceProjectLocalSettings, core.SourceProjectSettings),
		resolution.Current,
		resolution.Persisted,
		DifferenceProjectOverride,
	)
	resolution.IDEDrift = compareIDEContexts(
		filterSources(read.Sources, core.SourceVSCodeSettings, core.SourceCursorSettings),
		resolution.Current,
		resolution.Persisted,
	)

	return resolution
}

func resolveConfig(name string, sources []Source, keepExistingOnEqual bool) core.ResolvedConfig {
	resolved := core.ResolvedConfig{
		Name:   name,
		Values: make(map[string]core.ResolvedValue),
	}

	for _, src := range sources {
		for _, variable := range core.AllManagedNames() {
			value, ok := src.Values[variable]
			if !ok {
				continue
			}
			existing, exists := resolved.Values[variable]
			if exists && keepExistingOnEqual && existing.Value == value.Value {
				continue
			}

			next := core.ResolvedValue{
				Name:   value.Name,
				Value:  value.Value,
				Source: value.Source,
				Secret: value.Secret,
			}
			if exists {
				next.Shadowed = append(next.Shadowed, core.ConfigValue{
					Name:   existing.Name,
					Value:  existing.Value,
					Source: existing.Source,
					Secret: existing.Secret,
				})
				next.Shadowed = append(next.Shadowed, existing.Shadowed...)
			}
			resolved.Values[variable] = next
		}
	}

	return resolved
}

func compareCurrentAndPersisted(current, persisted core.ResolvedConfig) []RuntimeDifference {
	names := orderedManagedNamesFromMaps(current.Values, persisted.Values)
	differences := make([]RuntimeDifference, 0)
	for _, name := range names {
		currentValue, currentOK := current.Values[name]
		persistedValue, persistedOK := persisted.Values[name]
		switch {
		case currentOK && !persistedOK:
			differences = append(differences, RuntimeDifference{
				Kind:    DifferenceCurrentOnly,
				Name:    name,
				Current: resolvedPtr(currentValue),
			})
		case !currentOK && persistedOK:
			differences = append(differences, RuntimeDifference{
				Kind:      DifferencePersistedOnly,
				Name:      name,
				Persisted: resolvedPtr(persistedValue),
			})
		case currentOK && persistedOK && currentValue.Value != persistedValue.Value:
			differences = append(differences, RuntimeDifference{
				Kind:      DifferenceCurrentDiffers,
				Name:      name,
				Current:   resolvedPtr(currentValue),
				Persisted: resolvedPtr(persistedValue),
			})
		}
	}
	return differences
}

func compareSideContexts(sources []Source, current, persisted core.ResolvedConfig, kind DifferenceKind) []SideContextDifference {
	differences := make([]SideContextDifference, 0)
	for _, src := range sources {
		for _, name := range orderedConfigValueNames(src.Values) {
			value := src.Values[name]
			global, comparedAgainst := preferredGlobal(name, current, persisted)
			if global != nil && global.Value == value.Value {
				continue
			}
			differences = append(differences, SideContextDifference{
				Kind:            kind,
				Name:            name,
				Context:         src.Label,
				ContextValue:    value,
				Global:          global,
				ComparedAgainst: comparedAgainst,
			})
		}
	}
	sortSideContextDifferences(differences)
	return differences
}

func compareIDEContexts(sources []Source, current, persisted core.ResolvedConfig) []SideContextDifference {
	differences := compareSideContexts(sources, current, persisted, DifferenceIDEDrift)
	for _, src := range sources {
		if src.SelectedModel == nil {
			continue
		}
		value := *src.SelectedModel
		global, comparedAgainst := preferredGlobal(value.Name, current, persisted)
		if global != nil && global.Value == value.Value {
			continue
		}
		differences = append(differences, SideContextDifference{
			Kind:            DifferenceIDEDrift,
			Name:            value.Name,
			Context:         value.Source,
			ContextValue:    value,
			Global:          global,
			ComparedAgainst: comparedAgainst,
		})
	}
	sortSideContextDifferences(differences)
	return differences
}

func preferredGlobal(name string, current, persisted core.ResolvedConfig) (*core.ResolvedValue, string) {
	if value, ok := current.Values[name]; ok {
		return resolvedPtr(value), current.Name
	}
	if value, ok := persisted.Values[name]; ok {
		return resolvedPtr(value), persisted.Name
	}
	return nil, ""
}

func filterSources(sources []Source, kinds ...core.SourceKind) []Source {
	allowed := make(map[core.SourceKind]bool, len(kinds))
	for _, kind := range kinds {
		allowed[kind] = true
	}
	out := make([]Source, 0, len(sources))
	for _, src := range sources {
		if allowed[src.Label.Kind] {
			out = append(out, src)
		}
	}
	return out
}

func orderedConfigValueNames(values map[string]core.ConfigValue) []string {
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
		if seen[name] {
			continue
		}
		extras = append(extras, name)
	}
	sort.Strings(extras)
	return append(names, extras...)
}

func sortSideContextDifferences(differences []SideContextDifference) {
	sort.SliceStable(differences, func(i, j int) bool {
		if differences[i].Context.String() != differences[j].Context.String() {
			return differences[i].Context.String() < differences[j].Context.String()
		}
		if differences[i].Name != differences[j].Name {
			return differences[i].Name < differences[j].Name
		}
		return differences[i].ContextValue.Source.Detail < differences[j].ContextValue.Source.Detail
	})
}
