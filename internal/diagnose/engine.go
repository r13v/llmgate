package diagnose

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/r13v/llmgate/internal/config"
	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/gateway"
	"github.com/r13v/llmgate/internal/system"
)

const defaultCommandTimeout = 5 * time.Second

type Options struct {
	NetworkChecks  bool
	Gateway        gateway.Client
	CommandTimeout time.Duration
}

type Result struct {
	Sections                          []core.DiagnosticSection
	Findings                          []core.DiagnosticFinding
	Read                              config.ReadResult
	Resolution                        config.Resolution
	RepairableStaleShellModelWarnings []RepairableStaleShellModelWarning
}

type RepairableStaleShellModelWarning struct {
	Name         string
	StaleValue   core.ResolvedValue
	CurrentValue core.ResolvedValue
	Source       core.SourceLabel
	Summary      string
}

func Run(ctx context.Context, sys system.System, read config.ReadResult, opts Options) (Result, error) {
	resolution := config.Resolve(read)
	evaluations := evaluateGlobalContexts(ctx, resolution, opts)
	multipleContexts := len(evaluations) > 1

	result := Result{
		Read:       read,
		Resolution: resolution,
	}
	result.Sections = append(result.Sections, buildClaudeCLISection(ctx, sys, opts))
	result.Sections = append(result.Sections, buildClaudeConfigSection(resolution))
	if section, ok := buildConflictSection(resolution.Conflicts); ok {
		result.Sections = append(result.Sections, section)
	}
	result.Sections = append(result.Sections, buildRuntimeSection(resolution.Runtime))
	if section, ok := buildSourceIssueSection(resolution.SourceIssues); ok {
		result.Sections = append(result.Sections, section)
	}
	result.Sections = append(result.Sections, buildProjectOverrideSection(resolution.ProjectOverrides))

	var sideGatewayFailures []sideGatewayFailure
	for _, evaluation := range evaluations {
		result.Sections = append(result.Sections, buildGatewaySection(evaluation, multipleContexts, hasOtherUsable(evaluations, evaluation.name)))
	}
	for _, evaluation := range evaluations {
		section, repairable := buildModelsSection(evaluation, multipleContexts, hasOtherUsable(evaluations, evaluation.name), resolution.Current)
		result.Sections = append(result.Sections, section)
		result.RepairableStaleShellModelWarnings = append(result.RepairableStaleShellModelWarnings, repairable...)
	}
	for _, evaluation := range evaluations {
		result.Sections = append(result.Sections, buildProbesSection(evaluation, multipleContexts, hasOtherUsable(evaluations, evaluation.name)))
	}
	if section, ok := buildIDEConfigSection(read, resolution.IDEDrift); ok {
		result.Sections = append(result.Sections, section)
	}
	if section, failures, ok := buildProjectConfigValidationSection(ctx, read, resolution, opts); ok {
		result.Sections = append(result.Sections, section)
		sideGatewayFailures = append(sideGatewayFailures, failures...)
	}
	if section, failures, ok := buildIDEConfigValidationSection(ctx, read, resolution, opts); ok {
		result.Sections = append(result.Sections, section)
		sideGatewayFailures = append(sideGatewayFailures, failures...)
	}
	result.Sections = append(result.Sections, buildWriteTargetsSection(read.WriteTargets))
	result.Findings = buildDiagnosticFindings(resolution, evaluations, sideGatewayFailures, multipleContexts)

	return result, nil
}

func (r Result) Status() core.DiagnosticStatus {
	return core.AggregateSections(r.Sections)
}

func (r Result) HasRepairableStaleShellModelWarnings() bool {
	return len(r.RepairableStaleShellModelWarnings) > 0
}

func buildClaudeCLISection(ctx context.Context, sys system.System, opts Options) core.DiagnosticSection {
	timeout := opts.CommandTimeout
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	version, err := system.ClaudeVersion(commandCtx, sys.Commands)
	check := core.DiagnosticCheck{
		ID:     "claude-cli.version",
		Title:  "claude --version",
		Status: core.StatusOK,
	}
	if err != nil {
		check.Status = core.StatusWARN
		check.Summary = "Claude Code CLI check failed"
		check.Details = []string{err.Error()}
		if version != "" {
			check.Details = append(check.Details, "output: "+version)
		}
	} else {
		check.Summary = "Claude Code CLI is available"
		if version != "" {
			check.Details = []string{"version: " + version}
		}
	}

	return core.DiagnosticSection{
		ID:     sectionClaudeCLI,
		Title:  "Claude Code CLI",
		Checks: []core.DiagnosticCheck{check},
	}
}

func evaluateGlobalContexts(ctx context.Context, resolution config.Resolution, opts Options) []contextEvaluation {
	contexts := []contextEvaluation{
		evaluateContext(ctx, contextCurrent, resolution.Current, opts),
	}
	if contextsDiffer(resolution.Current, resolution.Persisted) {
		contexts = append(contexts, evaluateContext(ctx, contextPersisted, resolution.Persisted, opts))
	}
	return contexts
}

func evaluateContext(ctx context.Context, name string, resolved core.ResolvedConfig, opts Options) contextEvaluation {
	evaluation := contextEvaluation{
		name:          name,
		config:        resolved,
		networkChecks: opts.NetworkChecks,
		models:        resolvedModels(resolved),
	}
	if token, ok := resolved.Get(core.VarAnthropicAuthToken); ok {
		evaluation.token = token.Value
		evaluation.hasToken = true
	}
	if baseURL, ok := resolved.Get(core.VarAnthropicBaseURL); ok {
		evaluation.baseURL = baseURL.Value
		evaluation.hasBaseURL = true
	}

	if !opts.NetworkChecks || !evaluation.hasToken || !evaluation.hasBaseURL {
		evaluation.gatewaySkipped = true
		return evaluation
	}

	modelList, err := opts.Gateway.ListModels(ctx, evaluation.baseURL, evaluation.token, gateway.RequestOptions{})
	if err != nil {
		evaluation.gatewayErr = err
		return evaluation
	}
	evaluation.modelList = modelList
	evaluation.canListModels = true

	modelSet := stringSet(modelList.Models)
	allModelsAvailable := true
	for i := range evaluation.models {
		if !evaluation.models[i].present || !modelSet[evaluation.models[i].value.Value] {
			allModelsAvailable = false
			continue
		}
		evaluation.models[i].available = true
	}

	for _, model := range uniqueAvailableModels(evaluation.models) {
		result, probeErr := opts.Gateway.ProbeModel(ctx, evaluation.baseURL, evaluation.token, model, gateway.RequestOptions{})
		evaluation.probes = append(evaluation.probes, probeEvaluation{
			model:  model,
			result: result,
			err:    probeErr,
		})
	}

	evaluation.usable = allModelsAvailable && probesSucceeded(evaluation.probes)
	return evaluation
}

func resolvedModels(resolved core.ResolvedConfig) []modelEvaluation {
	models := make([]modelEvaluation, 0, len(modelVariables))
	for _, name := range modelVariables {
		value, ok := resolved.Get(name)
		models = append(models, modelEvaluation{
			name:    name,
			value:   value,
			present: ok,
		})
	}
	return models
}

func uniqueAvailableModels(models []modelEvaluation) []string {
	seen := make(map[string]bool, len(models))
	unique := make([]string, 0, len(models))
	for _, model := range models {
		if !model.present || !model.available || seen[model.value.Value] {
			continue
		}
		seen[model.value.Value] = true
		unique = append(unique, model.value.Value)
	}
	sort.Strings(unique)
	return unique
}

func probesSucceeded(probes []probeEvaluation) bool {
	if len(probes) == 0 {
		return false
	}
	for _, probe := range probes {
		if probe.err != nil {
			return false
		}
	}
	return true
}

func contextsDiffer(current, persisted core.ResolvedConfig) bool {
	for _, name := range gatewayContextVariables {
		currentValue, currentOK := current.Get(name)
		persistedValue, persistedOK := persisted.Get(name)
		if currentOK != persistedOK {
			return true
		}
		if currentOK && currentValue.Value != persistedValue.Value {
			return true
		}
	}
	return false
}

func hasOtherUsable(evaluations []contextEvaluation, name string) bool {
	for _, evaluation := range evaluations {
		if evaluation.name != name && evaluation.usable {
			return true
		}
	}
	return false
}

func repairableStaleShellModel(model modelEvaluation, current core.ResolvedConfig) (RepairableStaleShellModelWarning, bool) {
	if !model.present || !isModelVariable(model.name) || model.value.Source.Kind != core.SourceShellProfile || model.value.Source.Path == "" {
		return RepairableStaleShellModelWarning{}, false
	}
	currentValue, ok := current.Get(model.name)
	if !ok || currentValue.Value == model.value.Value {
		return RepairableStaleShellModelWarning{}, false
	}
	return RepairableStaleShellModelWarning{
		Name:         model.name,
		StaleValue:   model.value,
		CurrentValue: currentValue,
		Source:       model.value.Source,
		Summary:      fmt.Sprintf("%s in %s is unavailable and differs from the current environment", model.name, model.value.Source.Path),
	}, true
}

func buildProjectConfigValidationSection(ctx context.Context, read config.ReadResult, resolution config.Resolution, opts Options) (core.DiagnosticSection, []sideGatewayFailure, bool) {
	if !opts.NetworkChecks {
		return core.DiagnosticSection{}, nil, false
	}

	sources := orderedSideSources(read.Sources, core.SourceProjectLocalSettings, core.SourceProjectSettings)
	if len(sources) == 0 {
		return core.DiagnosticSection{}, nil, false
	}

	checks := make([]core.DiagnosticCheck, 0)
	var failures []sideGatewayFailure
	for _, source := range sources {
		side := sideContextFromSource(source, resolution.Current)
		validation := validateSideContext(ctx, side, opts, "project")
		checks = append(checks, validation.Checks...)
		if validation.GatewayFailure != nil {
			failures = append(failures, *validation.GatewayFailure)
		}
	}

	return core.DiagnosticSection{
		ID:     sectionProjectConfigValidation,
		Title:  "Project Config Validation",
		Checks: checks,
	}, failures, true
}

func buildIDEConfigValidationSection(ctx context.Context, read config.ReadResult, resolution config.Resolution, opts Options) (core.DiagnosticSection, []sideGatewayFailure, bool) {
	if !opts.NetworkChecks {
		return core.DiagnosticSection{}, nil, false
	}

	sources := orderedSideSources(read.Sources, core.SourceVSCodeSettings, core.SourceCursorSettings)
	if len(sources) == 0 {
		return core.DiagnosticSection{}, nil, false
	}

	checks := make([]core.DiagnosticCheck, 0)
	var failures []sideGatewayFailure
	for _, source := range sources {
		side := sideContextFromSource(source, resolution.Current)
		validation := validateSideContext(ctx, side, opts, "IDE")
		checks = append(checks, validation.Checks...)
		if validation.GatewayFailure != nil {
			failures = append(failures, *validation.GatewayFailure)
		}
	}

	return core.DiagnosticSection{
		ID:     sectionIDEConfigValidation,
		Title:  "IDE Config Validation",
		Checks: checks,
	}, failures, true
}

func sideContextFromSource(source config.Source, global core.ResolvedConfig) sideValidationContext {
	values := make(map[string]core.ResolvedValue)
	for _, name := range gatewayContextVariables {
		if globalValue, ok := global.Get(name); ok {
			values[name] = globalValue
		}
	}
	for _, value := range source.Values {
		values[value.Name] = core.ResolvedValue{
			Name:   value.Name,
			Value:  value.Value,
			Source: value.Source,
			Secret: value.Secret,
		}
	}
	if source.SelectedModel != nil {
		value := *source.SelectedModel
		values[value.Name] = core.ResolvedValue{
			Name:   value.Name,
			Value:  value.Value,
			Source: value.Source,
			Secret: value.Secret,
		}
	}

	var models []core.ConfigValue
	for _, name := range modelVariables {
		if value, ok := source.Values[name]; ok {
			models = append(models, value)
		}
	}
	if source.SelectedModel != nil {
		models = append(models, *source.SelectedModel)
	}
	models = uniqueConfigValues(models)

	token := values[core.VarAnthropicAuthToken]
	baseURL := values[core.VarAnthropicBaseURL]
	return sideValidationContext{
		name:    source.Label.String(),
		source:  source.Label,
		models:  models,
		token:   token.Value,
		baseURL: baseURL.Value,
	}
}

func validateSideContext(ctx context.Context, side sideValidationContext, opts Options, label string) sideValidationResult {
	if side.token == "" || side.baseURL == "" {
		return sideValidationResult{Checks: []core.DiagnosticCheck{{
			ID:      sideCheckID(label, side.source, "credentials"),
			Title:   side.name,
			Status:  core.StatusWARN,
			Summary: fmt.Sprintf("%s gateway credentials are insufficient for validation", label),
			Details: sideCredentialDetails(side),
		}}}
	}

	modelList, err := opts.Gateway.ListModels(ctx, side.baseURL, side.token, gateway.RequestOptions{})
	if err != nil {
		checkID := sideCheckID(label, side.source, "gateway")
		return sideValidationResult{
			Checks: []core.DiagnosticCheck{{
				ID:      checkID,
				Title:   side.name,
				Status:  core.StatusWARN,
				Summary: fmt.Sprintf("%s gateway validation failed", label),
				Details: gatewayFailureDetails(err),
			}},
			GatewayFailure: &sideGatewayFailure{
				Source:  side.source,
				Name:    side.name,
				Err:     err,
				CheckID: checkID,
			},
		}
	}

	checks := []core.DiagnosticCheck{{
		ID:      sideCheckID(label, side.source, "gateway"),
		Title:   side.name,
		Status:  core.StatusOK,
		Summary: fmt.Sprintf("%s gateway validation succeeded", label),
		Details: []string{modelList.Summary},
	}}

	modelSet := stringSet(modelList.Models)
	if len(side.models) == 0 {
		checks = append(checks, core.DiagnosticCheck{
			ID:      sideCheckID(label, side.source, "models"),
			Title:   side.name,
			Status:  core.StatusOK,
			Summary: fmt.Sprintf("%s has no model override to validate", label),
		})
		return sideValidationResult{Checks: checks}
	}
	for _, model := range side.models {
		status := core.StatusOK
		summary := fmt.Sprintf("%s model is available", model.Name)
		if !modelSet[model.Value] {
			status = core.StatusWARN
			summary = fmt.Sprintf("%s model is unavailable", model.Name)
		}
		checks = append(checks, core.DiagnosticCheck{
			ID:      sideCheckID(label, model.Source, "model."+model.Name),
			Title:   model.Name,
			Status:  status,
			Summary: summary,
			Details: []string{"model: " + displayValue(model.Value, model.Secret), "source: " + model.Source.String()},
		})
	}
	return sideValidationResult{Checks: checks}
}

func sideCredentialDetails(side sideValidationContext) []string {
	var details []string
	if side.token == "" {
		details = append(details, core.VarAnthropicAuthToken+": <unset>")
	}
	if side.baseURL == "" {
		details = append(details, core.VarAnthropicBaseURL+": <unset>")
	}
	return details
}

func sideCheckID(label string, source core.SourceLabel, suffix string) string {
	return fmt.Sprintf("%s.%s.%s", label, source.Kind, suffix)
}

func uniqueConfigValues(values []core.ConfigValue) []core.ConfigValue {
	seen := make(map[string]bool, len(values))
	unique := make([]core.ConfigValue, 0, len(values))
	for _, value := range values {
		key := value.Name + "\x00" + value.Value + "\x00" + value.Source.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, value)
	}
	sort.SliceStable(unique, func(i, j int) bool {
		if unique[i].Name != unique[j].Name {
			return unique[i].Name < unique[j].Name
		}
		return unique[i].Value < unique[j].Value
	})
	return unique
}

func isModelVariable(name string) bool {
	for _, variable := range modelVariables {
		if variable == name {
			return true
		}
	}
	return false
}
