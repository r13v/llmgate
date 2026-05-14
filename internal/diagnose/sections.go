package diagnose

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/r13v/llmgate/internal/config"
	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/gateway"
	"github.com/r13v/llmgate/internal/redact"
)

const (
	sectionClaudeCLI               = "claude-cli"
	sectionClaudeConfig            = "claude-config"
	sectionConfigSourceConflicts   = "config-source-conflicts"
	sectionRuntimeEnvironment      = "runtime-environment"
	sectionConfigSources           = "config-sources"
	sectionProjectOverrides        = "project-overrides"
	sectionGateway                 = "gateway"
	sectionModels                  = "models"
	sectionModelProbes             = "model-probes"
	sectionIDEConfig               = "ide-config"
	sectionProjectConfigValidation = "project-config-validation"
	sectionIDEConfigValidation     = "ide-config-validation"
	sectionWriteTargets            = "write-targets"

	contextCurrent   = "current environment"
	contextPersisted = "persisted config for new sessions"
)

var (
	gatewayContextVariables = []string{
		core.VarAnthropicAuthToken,
		core.VarAnthropicBaseURL,
		core.VarAnthropicModel,
		core.VarAnthropicDefaultHaikuModel,
		core.VarAnthropicDefaultSonnetModel,
		core.VarAnthropicDefaultOpusModel,
	}

	modelVariables = []string{
		core.VarAnthropicModel,
		core.VarAnthropicDefaultHaikuModel,
		core.VarAnthropicDefaultSonnetModel,
		core.VarAnthropicDefaultOpusModel,
	}
)

type contextEvaluation struct {
	name           string
	config         core.ResolvedConfig
	networkChecks  bool
	token          string
	baseURL        string
	hasToken       bool
	hasBaseURL     bool
	modelList      gateway.ModelListResult
	gatewayErr     error
	models         []modelEvaluation
	probes         []probeEvaluation
	usable         bool
	canListModels  bool
	gatewaySkipped bool
}

type modelEvaluation struct {
	name      string
	value     core.ResolvedValue
	present   bool
	available bool
}

type probeEvaluation struct {
	model  string
	result gateway.ProbeResult
	err    error
}

type sideValidationContext struct {
	name    string
	source  core.SourceLabel
	models  []core.ConfigValue
	token   string
	baseURL string
}

type sideValidationResult struct {
	Checks         []core.DiagnosticCheck
	GatewayFailure *sideGatewayFailure
}

type sideGatewayFailure struct {
	Source  core.SourceLabel
	Name    string
	Err     error
	CheckID string
}

func buildClaudeConfigSection(resolution config.Resolution) core.DiagnosticSection {
	checks := make([]core.DiagnosticCheck, 0, len(core.RequiredValues))
	for _, required := range core.RequiredValues {
		current, currentOK := resolution.Current.Get(required.Name)
		persisted, persistedOK := resolution.Persisted.Get(required.Name)
		status := core.StatusOK
		summary := fmt.Sprintf("%s is present in current and persisted config", required.Name)
		var details []string

		switch {
		case currentOK && persistedOK:
			details = append(details,
				"current: "+resolvedDisplay(current),
				"persisted: "+resolvedDisplay(persisted),
			)
		case currentOK:
			status = core.StatusWARN
			summary = fmt.Sprintf("%s exists only in the current environment", required.Name)
			details = append(details, "current: "+resolvedDisplay(current))
		case persistedOK:
			status = core.StatusWARN
			summary = fmt.Sprintf("%s exists only in persisted config for new sessions", required.Name)
			details = append(details, "persisted: "+resolvedDisplay(persisted))
		default:
			status = core.StatusFAIL
			summary = fmt.Sprintf("%s is missing from current and persisted config", required.Name)
		}

		checks = append(checks, core.DiagnosticCheck{
			ID:      "claude-config." + required.Name,
			Title:   required.Meaning,
			Status:  status,
			Summary: summary,
			Details: details,
		})
	}

	return core.DiagnosticSection{
		ID:     sectionClaudeConfig,
		Title:  "Claude Code Config",
		Checks: checks,
	}
}

func buildConflictSection(conflicts []config.ConflictIssue) (core.DiagnosticSection, bool) {
	if len(conflicts) == 0 {
		return core.DiagnosticSection{}, false
	}

	checks := make([]core.DiagnosticCheck, 0, len(conflicts))
	for i, conflict := range conflicts {
		details := make([]string, 0)
		if conflict.Effective.Name != "" {
			details = append(details, "effective: "+resolvedDisplay(conflict.Effective))
		}
		for _, value := range conflict.Values {
			details = append(details, "source: "+configValueDisplay(value))
		}
		if conflict.Issue.Summary != "" {
			details = append(details, conflict.Issue.Summary)
		}

		checks = append(checks, core.DiagnosticCheck{
			ID:      conflictCheckID(i, conflict.Name),
			Title:   conflict.Name,
			Status:  conflict.Status,
			Summary: conflict.Summary,
			Details: details,
		})
	}

	return core.DiagnosticSection{
		ID:     sectionConfigSourceConflicts,
		Title:  "Config Source Conflicts",
		Checks: checks,
	}, true
}

func buildRuntimeSection(differences []config.RuntimeDifference) core.DiagnosticSection {
	if len(differences) == 0 {
		return core.DiagnosticSection{
			ID:    sectionRuntimeEnvironment,
			Title: "Runtime Environment",
			Checks: []core.DiagnosticCheck{{
				ID:      "runtime.match",
				Title:   "Runtime matches persisted config",
				Status:  core.StatusOK,
				Summary: "current environment and persisted config match for managed values",
			}},
		}
	}

	checks := make([]core.DiagnosticCheck, 0, len(differences))
	for _, difference := range differences {
		checks = append(checks, core.DiagnosticCheck{
			ID:      "runtime." + difference.Name,
			Title:   difference.Name,
			Status:  core.StatusWARN,
			Summary: runtimeSummary(difference),
			Details: runtimeDetails(difference),
		})
	}

	return core.DiagnosticSection{
		ID:     sectionRuntimeEnvironment,
		Title:  "Runtime Environment",
		Checks: checks,
	}
}

func buildSourceIssueSection(issues []config.SourceIssue) (core.DiagnosticSection, bool) {
	if len(issues) == 0 {
		return core.DiagnosticSection{}, false
	}

	checks := make([]core.DiagnosticCheck, 0, len(issues))
	for i, issue := range issues {
		checks = append(checks, core.DiagnosticCheck{
			ID:      fmt.Sprintf("config-source.%02d", i+1),
			Title:   issue.Source.String(),
			Status:  issue.Status,
			Summary: issue.Summary,
			Details: []string{"source: " + issue.Source.String()},
		})
	}

	return core.DiagnosticSection{
		ID:     sectionConfigSources,
		Title:  "Config Sources",
		Checks: checks,
	}, true
}

func buildProjectOverrideSection(differences []config.SideContextDifference) core.DiagnosticSection {
	if len(differences) == 0 {
		return core.DiagnosticSection{
			ID:    sectionProjectOverrides,
			Title: "Project Overrides",
			Checks: []core.DiagnosticCheck{{
				ID:      "project-overrides.none",
				Title:   "Project overrides",
				Status:  core.StatusOK,
				Summary: "none detected",
			}},
		}
	}

	checks := make([]core.DiagnosticCheck, 0, len(differences))
	for i, difference := range differences {
		details := []string{
			"project location: " + difference.Context.String(),
			"project value: " + configValueDisplay(difference.ContextValue),
			"reason: project settings override the global effective value for this workspace",
			"manual fix: update or remove the project setting if this override is not intentional",
		}
		if difference.Global != nil {
			details = append(details, "global "+difference.ComparedAgainst+": "+resolvedDisplay(*difference.Global))
		} else {
			details = append(details, "global "+difference.ComparedAgainst+": <unset>")
		}

		checks = append(checks, core.DiagnosticCheck{
			ID:      fmt.Sprintf("project-overrides.%02d.%s", i+1, difference.Name),
			Title:   difference.Name,
			Status:  core.StatusWARN,
			Summary: fmt.Sprintf("%s differs in project settings", difference.Name),
			Details: details,
		})
	}

	return core.DiagnosticSection{
		ID:     sectionProjectOverrides,
		Title:  "Project Overrides",
		Checks: checks,
	}
}

func buildGatewaySection(evaluation contextEvaluation, multiple, hasOtherUsable bool) core.DiagnosticSection {
	status := core.StatusOK
	summary := evaluation.modelList.Summary
	var details []string

	switch {
	case !evaluation.networkChecks:
		status = core.StatusSKIP
		summary = "network checks disabled"
	case !evaluation.hasToken || !evaluation.hasBaseURL:
		status = core.StatusSKIP
		summary = "configure token and base URL first"
		details = missingCredentialDetails(evaluation)
	case evaluation.gatewayErr != nil:
		status = downgradedStatus(core.StatusFAIL, hasOtherUsable)
		summary = "gateway validation failed"
		details = append(details, gatewayFailureDetails(evaluation.gatewayErr)...)
		if hasOtherUsable {
			details = append(details, "another configuration context is valid")
		}
	default:
		if summary == "" {
			summary = "gateway model list succeeded"
		}
		details = append(details, "model list URL: "+evaluation.modelList.URL)
		if evaluation.modelList.FallbackUsed {
			details = append(details, "fallback URL: "+evaluation.modelList.FallbackURL)
		}
	}

	return core.DiagnosticSection{
		ID:    sectionID(sectionGateway, evaluation, multiple),
		Title: sectionTitle("Gateway", evaluation, multiple),
		Checks: []core.DiagnosticCheck{{
			ID:      sectionID("gateway.validation", evaluation, multiple),
			Title:   "Gateway validation",
			Status:  status,
			Summary: summary,
			Details: details,
		}},
	}
}

func buildModelsSection(evaluation contextEvaluation, multiple, hasOtherUsable bool, current core.ResolvedConfig) (core.DiagnosticSection, []RepairableStaleShellModelWarning) {
	checks := make([]core.DiagnosticCheck, 0, len(evaluation.models))
	var repairable []RepairableStaleShellModelWarning

	if !evaluation.networkChecks {
		checks = append(checks, skippedCheck("models.skipped", "Model availability", "network checks disabled"))
	} else if !evaluation.canListModels {
		checks = append(checks, skippedCheck("models.skipped", "Model availability", "validate gateway before model availability"))
	} else {
		modelSet := stringSet(evaluation.modelList.Models)
		for _, model := range evaluation.models {
			status := core.StatusOK
			summary := fmt.Sprintf("%s is available", model.name)
			details := []string{"model: " + displayValue(model.value.Value, model.value.Secret)}

			switch {
			case !model.present:
				status = downgradedStatus(core.StatusFAIL, hasOtherUsable)
				summary = fmt.Sprintf("%s is missing", model.name)
				details = nil
			case !modelSet[model.value.Value]:
				status = downgradedStatus(core.StatusFAIL, hasOtherUsable)
				summary = fmt.Sprintf("%s model is unavailable", model.name)
				details = append(details, "source: "+model.value.Source.String())
				if hasOtherUsable {
					details = append(details, "another configuration context is valid")
				}
				if status == core.StatusWARN {
					if warning, ok := repairableStaleShellModel(model, current); ok {
						repairable = append(repairable, warning)
						details = append(details, "repairable: stale shell model can be updated from the current environment")
					}
				}
			default:
				details = append(details, "source: "+model.value.Source.String())
			}

			checks = append(checks, core.DiagnosticCheck{
				ID:      sectionID("models."+model.name, evaluation, multiple),
				Title:   modelTitle(model.name),
				Status:  status,
				Summary: summary,
				Details: details,
			})
		}
	}

	return core.DiagnosticSection{
		ID:     sectionID(sectionModels, evaluation, multiple),
		Title:  sectionTitle("Models", evaluation, multiple),
		Checks: checks,
	}, repairable
}

func buildProbesSection(evaluation contextEvaluation, multiple, hasOtherUsable bool) core.DiagnosticSection {
	checks := make([]core.DiagnosticCheck, 0, len(evaluation.probes))
	if !evaluation.networkChecks {
		checks = append(checks, skippedCheck("model-probes.skipped", "Model probes", "network checks disabled"))
	} else if !evaluation.canListModels {
		checks = append(checks, skippedCheck("model-probes.skipped", "Model probes", "validate gateway before probing models"))
	} else if len(evaluation.probes) == 0 {
		checks = append(checks, skippedCheck("model-probes.none", "Model probes", "no available selected models to probe"))
	} else {
		for _, probe := range evaluation.probes {
			status := core.StatusOK
			summary := fmt.Sprintf("probe accepted model %q", probe.model)
			var details []string
			if probe.result.Summary != "" {
				summary = probe.result.Summary
			}
			if probe.result.URL != "" {
				details = append(details, "completions URL: "+probe.result.URL)
			}
			if probe.err != nil {
				status = downgradedStatus(core.StatusFAIL, hasOtherUsable)
				summary = fmt.Sprintf("probe failed for model %q", probe.model)
				details = gatewayFailureDetails(probe.err)
				if hasOtherUsable {
					details = append(details, "another configuration context is valid")
				}
			}
			checks = append(checks, core.DiagnosticCheck{
				ID:      sectionID("model-probes."+probe.model, evaluation, multiple),
				Title:   probe.model,
				Status:  status,
				Summary: summary,
				Details: details,
			})
		}
	}

	return core.DiagnosticSection{
		ID:     sectionID(sectionModelProbes, evaluation, multiple),
		Title:  sectionTitle("Model Probes", evaluation, multiple),
		Checks: checks,
	}
}

func buildIDEConfigSection(read config.ReadResult, differences []config.SideContextDifference) (core.DiagnosticSection, bool) {
	if !hasIDESource(read.Sources) {
		return core.DiagnosticSection{}, false
	}
	if len(differences) == 0 {
		return core.DiagnosticSection{
			ID:    sectionIDEConfig,
			Title: "IDE Config",
			Checks: []core.DiagnosticCheck{{
				ID:      "ide-config.match",
				Title:   "IDE config",
				Status:  core.StatusOK,
				Summary: "IDE Claude settings match terminal config",
			}},
		}, true
	}

	checks := make([]core.DiagnosticCheck, 0, len(differences))
	for i, difference := range differences {
		details := []string{
			"IDE value: " + configValueDisplay(difference.ContextValue),
		}
		if difference.Global != nil {
			details = append(details, "global "+difference.ComparedAgainst+": "+resolvedDisplay(*difference.Global))
		} else {
			details = append(details, "global "+difference.ComparedAgainst+": <unset>")
		}
		checks = append(checks, core.DiagnosticCheck{
			ID:      ideDriftCheckID(i, difference.Name),
			Title:   difference.Name,
			Status:  core.StatusWARN,
			Summary: fmt.Sprintf("%s differs in IDE settings", difference.Name),
			Details: details,
		})
	}

	return core.DiagnosticSection{
		ID:     sectionIDEConfig,
		Title:  "IDE Config",
		Checks: checks,
	}, true
}

func buildWriteTargetsSection(targets []core.WriteTarget) core.DiagnosticSection {
	checks := make([]core.DiagnosticCheck, 0, len(targets))
	for i, target := range targets {
		status := core.StatusOK
		summary := "writable target detected"
		if !target.Writable {
			status = core.StatusSKIP
			summary = "manual target detected"
		}
		details := []string{
			"kind: " + string(target.Kind),
			fmt.Sprintf("exists: %t", target.Exists),
			fmt.Sprintf("sensitive: %t", target.Sensitive),
		}
		if target.Path != "" {
			details = append(details, "path: "+target.Path)
		}
		checks = append(checks, core.DiagnosticCheck{
			ID:      fmt.Sprintf("write-targets.%02d.%s", i+1, target.Kind),
			Title:   target.Title,
			Status:  status,
			Summary: summary,
			Details: details,
		})
	}
	if len(checks) == 0 {
		checks = append(checks, skippedCheck("write-targets.none", "Write targets", "no write targets detected"))
	}

	return core.DiagnosticSection{
		ID:     sectionWriteTargets,
		Title:  "Write Targets",
		Checks: checks,
	}
}

func runtimeSummary(difference config.RuntimeDifference) string {
	switch difference.Kind {
	case config.DifferenceCurrentOnly:
		return fmt.Sprintf("%s exists only in the current environment", difference.Name)
	case config.DifferencePersistedOnly:
		return fmt.Sprintf("%s exists only in persisted config for new sessions", difference.Name)
	case config.DifferenceCurrentDiffers:
		return fmt.Sprintf("%s differs between current and persisted config", difference.Name)
	default:
		return fmt.Sprintf("%s differs between runtime contexts", difference.Name)
	}
}

func runtimeDetails(difference config.RuntimeDifference) []string {
	var details []string
	if difference.Current != nil {
		details = append(details, "current: "+resolvedDisplay(*difference.Current))
	} else {
		details = append(details, "current: <unset>")
	}
	if difference.Persisted != nil {
		details = append(details, "persisted: "+resolvedDisplay(*difference.Persisted))
	} else {
		details = append(details, "persisted: <unset>")
	}
	return details
}

func missingCredentialDetails(evaluation contextEvaluation) []string {
	var details []string
	if !evaluation.hasToken {
		details = append(details, core.VarAnthropicAuthToken+": <unset>")
	}
	if !evaluation.hasBaseURL {
		details = append(details, core.VarAnthropicBaseURL+": <unset>")
	}
	return details
}

func gatewayFailureDetails(err error) []string {
	details := []string{"reason: " + err.Error()}
	var gatewayErr *gateway.Error
	if !errors.As(err, &gatewayErr) {
		return details
	}
	if gatewayErr.URL != "" {
		details = append(details, "request URL: "+gatewayErr.URL)
	}
	if gatewayErr.Kind != "" {
		details = append(details, "failure kind: "+string(gatewayErr.Kind))
	}
	if gatewayErr.StatusCode != 0 {
		details = append(details, fmt.Sprintf("HTTP status: %d", gatewayErr.StatusCode))
	}
	if meaning := gatewayFailureMeaning(gatewayErr); meaning != "" {
		details = append(details, "what it means: "+meaning)
	}
	return details
}

func gatewayFailureMeaning(err *gateway.Error) string {
	switch err.Kind {
	case gateway.FailureAuth:
		return "the gateway rejected the configured token; check ANTHROPIC_AUTH_TOKEN for this source"
	case gateway.FailureNetwork:
		return "llmgate could not reach the gateway; check the base URL, DNS, VPN/proxy, TLS, and network access"
	case gateway.FailureHTTP:
		return "the gateway returned a non-success HTTP response; inspect the gateway/upstream logs and base URL"
	case gateway.FailureInvalidJSON:
		return "the gateway response was not OpenAI-compatible JSON"
	case gateway.FailureEmptyModels:
		return "the gateway returned no usable model IDs"
	case gateway.FailureInvalidURL:
		return "the configured base URL is malformed; use an http(s) LiteLLM gateway root or /v1 URL"
	default:
		return ""
	}
}

func resolvedDisplay(value core.ResolvedValue) string {
	return fmt.Sprintf("%s=%s from %s", value.Name, displayValue(value.Value, value.Secret), value.Source.String())
}

func configValueDisplay(value core.ConfigValue) string {
	return fmt.Sprintf("%s=%s from %s", value.Name, displayValue(value.Value, value.Secret), value.Source.String())
}

func displayValue(value string, secret bool) string {
	if secret {
		return redact.MaskSecret(value)
	}
	if value == "" {
		return "<empty>"
	}
	return value
}

func sectionTitle(base string, evaluation contextEvaluation, multiple bool) string {
	if !multiple {
		return base
	}
	return base + " (" + evaluation.name + ")"
}

func sectionID(base string, evaluation contextEvaluation, multiple bool) string {
	if !multiple {
		return base
	}
	return base + "." + strings.ReplaceAll(evaluation.name, " ", "-")
}

func skippedCheck(id, title, summary string) core.DiagnosticCheck {
	return core.DiagnosticCheck{
		ID:      id,
		Title:   title,
		Status:  core.StatusSKIP,
		Summary: summary,
	}
}

func downgradedStatus(status core.DiagnosticStatus, hasOtherUsable bool) core.DiagnosticStatus {
	if hasOtherUsable && status == core.StatusFAIL {
		return core.StatusWARN
	}
	return status
}

func modelTitle(name string) string {
	if managed, ok := core.FindManagedValue(name); ok {
		return managed.Meaning
	}
	return name
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func hasIDESource(sources []config.Source) bool {
	for _, source := range sources {
		if source.Label.Kind == core.SourceVSCodeSettings || source.Label.Kind == core.SourceCursorSettings {
			return true
		}
	}
	return false
}

func hasGatewayContextValues(values map[string]core.ConfigValue, selectedModel *core.ConfigValue) bool {
	if selectedModel != nil {
		return true
	}
	for _, name := range gatewayContextVariables {
		if _, ok := values[name]; ok {
			return true
		}
	}
	return false
}

func orderedSideSources(sources []config.Source, kinds ...core.SourceKind) []config.Source {
	allowed := make(map[core.SourceKind]bool, len(kinds))
	for _, kind := range kinds {
		allowed[kind] = true
	}
	selected := make([]config.Source, 0)
	for _, source := range sources {
		if allowed[source.Label.Kind] && hasGatewayContextValues(source.Values, source.SelectedModel) {
			selected = append(selected, source)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		return selected[i].Label.String() < selected[j].Label.String()
	})
	return selected
}
