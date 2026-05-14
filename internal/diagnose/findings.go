package diagnose

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/r13v/llmgate/internal/config"
	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/gateway"
)

// OrderedFindings returns a copy of findings sorted from most to least severe.
func OrderedFindings(findings []core.DiagnosticFinding) []core.DiagnosticFinding {
	ordered := append([]core.DiagnosticFinding(nil), findings...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return findingSeverity(ordered[i].Status) > findingSeverity(ordered[j].Status)
	})
	return ordered
}

// CoveredCheckIDs returns the diagnostic check IDs referenced by findings.
func CoveredCheckIDs(findings []core.DiagnosticFinding) map[string]bool {
	covered := make(map[string]bool)
	for _, finding := range findings {
		for _, checkID := range finding.RelatedChecks {
			if checkID == "" {
				continue
			}
			covered[checkID] = true
		}
	}
	return covered
}

func buildDiagnosticFindings(resolution config.Resolution, evaluations []contextEvaluation, sideFailures []sideGatewayFailure, multipleContexts bool) []core.DiagnosticFinding {
	tokenSignals := authTokenRelatedSignals(resolution)
	findings := make([]core.DiagnosticFinding, 0)

	for _, evaluation := range evaluations {
		otherUsable := hasOtherUsable(evaluations, evaluation.name)
		if evaluation.gatewayErr != nil {
			checkID := sectionID("gateway.validation", evaluation, multipleContexts)
			findings = append(findings, gatewayErrorFinding(gatewayFindingInput{
				ID:            "gateway." + contextFindingSlug(evaluation.name),
				Prefix:        "Gateway",
				Scope:         evaluation.name,
				Err:           evaluation.gatewayErr,
				Status:        downgradedStatus(core.StatusFAIL, otherUsable),
				RelatedChecks: []string{checkID},
				TokenSignals:  tokenSignals,
			}))
		}
		for _, probe := range evaluation.probes {
			if probe.err == nil {
				continue
			}
			checkID := sectionID("model-probes."+probe.model, evaluation, multipleContexts)
			findings = append(findings, gatewayErrorFinding(gatewayFindingInput{
				ID:            "gateway.probe." + contextFindingSlug(evaluation.name) + "." + findingSlug(probe.model),
				Prefix:        "Gateway",
				Scope:         evaluation.name,
				Subject:       fmt.Sprintf("model %q", probe.model),
				Err:           probe.err,
				Status:        downgradedStatus(core.StatusFAIL, otherUsable),
				RelatedChecks: []string{checkID},
				TokenSignals:  tokenSignals,
			}))
		}
	}

	for _, failure := range sideFailures {
		findings = append(findings, gatewayErrorFinding(gatewayFindingInput{
			ID:            "gateway.side." + findingSlug(string(failure.Source.Kind)),
			Prefix:        sideGatewayPrefix(failure.Source),
			Scope:         failure.Name,
			Err:           failure.Err,
			Status:        core.StatusWARN,
			RelatedChecks: []string{failure.CheckID},
			TokenSignals:  tokenSignals,
		}))
	}

	findings = append(findings, buildConflictFindings(resolution.Conflicts)...)
	findings = append(findings, buildIDEDriftFindings(resolution.IDEDrift)...)

	return OrderedFindings(findings)
}

type gatewayFindingInput struct {
	ID            string
	Prefix        string
	Scope         string
	Subject       string
	Err           error
	Status        core.DiagnosticStatus
	RelatedChecks []string
	TokenSignals  relatedTokenSignals
}

type relatedTokenSignals struct {
	CheckIDs []string
	Evidence []string
}

type conflictFindingGroup struct {
	name           string
	status         core.DiagnosticStatus
	checkIDs       []string
	valueSet       map[string]bool
	sourceSet      map[string]bool
	effective      *core.ResolvedValue
	issueSummaries []string
}

func gatewayErrorFinding(input gatewayFindingInput) core.DiagnosticFinding {
	relatedChecks := append([]string(nil), input.RelatedChecks...)
	connectedTokenProblem := isGatewayAuthFailure(input.Err) && len(input.TokenSignals.CheckIDs) > 0
	if connectedTokenProblem {
		relatedChecks = append(relatedChecks, input.TokenSignals.CheckIDs...)
	}

	evidence := make([]string, 0)
	if input.Scope != "" {
		evidence = append(evidence, "context: "+input.Scope)
	}
	if input.Subject != "" {
		evidence = append(evidence, "subject: "+input.Subject)
	}
	evidence = append(evidence, gatewayErrorEvidence(input.Err)...)
	if connectedTokenProblem {
		evidence = append(evidence, input.TokenSignals.Evidence...)
	}

	return core.DiagnosticFinding{
		ID:            input.ID,
		Status:        input.Status,
		Title:         gatewayFindingTitle(input.Prefix, input.Err, input.Subject != ""),
		Summary:       gatewayFindingSummary(input.Err, connectedTokenProblem, input.Subject),
		Evidence:      uniqueStrings(evidence),
		Remediation:   gatewayFindingRemediation(input.Err, connectedTokenProblem),
		RelatedChecks: uniqueStrings(relatedChecks),
	}
}

func gatewayFindingTitle(prefix string, err error, probe bool) string {
	if prefix == "" {
		prefix = "Gateway"
	}
	var gatewayErr *gateway.Error
	if errors.As(err, &gatewayErr) {
		switch gatewayErr.Kind {
		case gateway.FailureAuth:
			return prefix + ": token rejected"
		case gateway.FailureNetwork:
			return prefix + ": unreachable"
		case gateway.FailureInvalidURL:
			return prefix + ": invalid base URL"
		case gateway.FailureInvalidJSON:
			return prefix + ": invalid response"
		case gateway.FailureEmptyModels:
			return prefix + ": no models returned"
		case gateway.FailureHTTP:
			if probe {
				return prefix + ": model probe failed"
			}
			return prefix + ": HTTP request failed"
		}
	}
	if probe {
		return prefix + ": model probe failed"
	}
	return prefix + ": validation failed"
}

func gatewayFindingSummary(err error, connectedTokenProblem bool, subject string) string {
	if connectedTokenProblem {
		return "The gateway rejected ANTHROPIC_AUTH_TOKEN, and configured token values differ across other sources."
	}

	var gatewayErr *gateway.Error
	if errors.As(err, &gatewayErr) {
		switch gatewayErr.Kind {
		case gateway.FailureAuth:
			return "The gateway rejected the configured ANTHROPIC_AUTH_TOKEN."
		case gateway.FailureNetwork:
			return "llmgate could not reach the configured gateway."
		case gateway.FailureInvalidURL:
			return "ANTHROPIC_BASE_URL is not a valid gateway URL."
		case gateway.FailureInvalidJSON:
			return "The gateway response was not OpenAI-compatible JSON."
		case gateway.FailureEmptyModels:
			return "The gateway returned no usable model IDs."
		case gateway.FailureHTTP:
			if subject != "" {
				return "The gateway rejected the probe for " + subject + "."
			}
			return "The gateway returned a non-success HTTP response."
		}
	}
	return "Gateway validation failed before llmgate could verify the configuration."
}

func gatewayErrorEvidence(err error) []string {
	var gatewayErr *gateway.Error
	if !errors.As(err, &gatewayErr) {
		return []string{"reason: " + err.Error()}
	}

	var evidence []string
	if gatewayErr.Operation != "" {
		evidence = append(evidence, "operation: "+gatewayErr.Operation)
	}
	if gatewayErr.URL != "" {
		evidence = append(evidence, "request URL: "+gatewayErr.URL)
	}
	if gatewayErr.Kind != "" {
		evidence = append(evidence, "failure kind: "+string(gatewayErr.Kind))
	}
	if gatewayErr.StatusCode != 0 {
		evidence = append(evidence, fmt.Sprintf("HTTP status: %d", gatewayErr.StatusCode))
	}
	if gatewayErr.Detail != "" {
		evidence = append(evidence, "gateway message: "+shortEvidence(gatewayErr.Detail))
	} else if gatewayErr.Err != nil {
		evidence = append(evidence, "gateway message: "+shortEvidence(gatewayErr.Err.Error()))
	}
	if gatewayErr.Cached {
		evidence = append(evidence, "cached failure: true")
	}
	return evidence
}

func gatewayFindingRemediation(err error, connectedTokenProblem bool) string {
	if connectedTokenProblem {
		return "Choose one active ANTHROPIC_AUTH_TOKEN, update the gateway-facing source, and remove stale token overrides."
	}

	var gatewayErr *gateway.Error
	if errors.As(err, &gatewayErr) {
		switch gatewayErr.Kind {
		case gateway.FailureAuth:
			return "Update ANTHROPIC_AUTH_TOKEN for the active source, or remove the stale override."
		case gateway.FailureNetwork:
			return "Check ANTHROPIC_BASE_URL, DNS, VPN/proxy, TLS, and network access."
		case gateway.FailureInvalidURL:
			return "Set ANTHROPIC_BASE_URL to an http(s) LiteLLM gateway root or /v1 URL."
		case gateway.FailureInvalidJSON:
			return "Inspect the gateway response and OpenAI-compatible model-list route."
		case gateway.FailureEmptyModels:
			return "Configure the gateway to expose at least one usable model ID."
		case gateway.FailureHTTP:
			return "Inspect the gateway/upstream logs, base URL, and selected model routing."
		}
	}
	return "Inspect the gateway error, update the active gateway configuration, and rerun diagnostics."
}

func isGatewayAuthFailure(err error) bool {
	var gatewayErr *gateway.Error
	return errors.As(err, &gatewayErr) && gatewayErr.Kind == gateway.FailureAuth
}

func authTokenRelatedSignals(resolution config.Resolution) relatedTokenSignals {
	var checkIDs []string
	var evidence []string

	var conflictSources []string
	for i, conflict := range resolution.Conflicts {
		if conflict.Name != core.VarAnthropicAuthToken {
			continue
		}
		checkIDs = append(checkIDs, conflictCheckID(i, conflict.Name))
		for _, value := range conflict.Values {
			conflictSources = append(conflictSources, value.Source.String())
		}
	}
	if len(conflictSources) > 0 {
		evidence = append(evidence, "related config: ANTHROPIC_AUTH_TOKEN differs across "+strings.Join(sortedUniqueStrings(conflictSources), ", "))
	}

	var ideSources []string
	for i, difference := range resolution.IDEDrift {
		if difference.Name != core.VarAnthropicAuthToken {
			continue
		}
		checkIDs = append(checkIDs, ideDriftCheckID(i, difference.Name))
		ideSources = append(ideSources, difference.Context.String())
	}
	if len(ideSources) > 0 {
		evidence = append(evidence, "related IDE: ANTHROPIC_AUTH_TOKEN differs in "+strings.Join(sortedUniqueStrings(ideSources), ", "))
	}

	return relatedTokenSignals{
		CheckIDs: uniqueStrings(checkIDs),
		Evidence: evidence,
	}
}

func buildConflictFindings(conflicts []config.ConflictIssue) []core.DiagnosticFinding {
	groups := make(map[string]*conflictFindingGroup)
	var order []string
	for i, conflict := range conflicts {
		g := groups[conflict.Name]
		if g == nil {
			g = &conflictFindingGroup{
				name:      conflict.Name,
				status:    core.StatusOK,
				valueSet:  make(map[string]bool),
				sourceSet: make(map[string]bool),
			}
			groups[conflict.Name] = g
			order = append(order, conflict.Name)
		}
		g.status = core.AggregateStatus(g.status, conflict.Status)
		g.checkIDs = append(g.checkIDs, conflictCheckID(i, conflict.Name))
		if conflict.Effective.Name != "" {
			effective := conflict.Effective
			g.effective = &effective
		}
		for _, value := range conflict.Values {
			g.valueSet[value.Value] = true
			g.sourceSet[value.Source.String()] = true
		}
		if conflict.Issue.Summary != "" {
			g.issueSummaries = append(g.issueSummaries, conflict.Issue.Summary)
		}
	}

	findings := make([]core.DiagnosticFinding, 0, len(order))
	for _, name := range order {
		g := groups[name]
		evidence := make([]string, 0)
		if len(g.valueSet) > 0 {
			evidence = append(evidence, fmt.Sprintf("distinct values: %d", len(g.valueSet)))
		}
		if len(g.sourceSet) > 0 {
			evidence = append(evidence, "sources: "+strings.Join(sortedKeys(g.sourceSet), ", "))
		}
		if g.effective != nil {
			evidence = append(evidence, "effective source: "+g.effective.Source.String())
		}
		for _, summary := range sortedUniqueStrings(g.issueSummaries) {
			evidence = append(evidence, "issue: "+summary)
		}

		findings = append(findings, core.DiagnosticFinding{
			ID:            "config-conflict." + findingSlug(name),
			Status:        g.status,
			Title:         "Config: " + name + " differs across sources",
			Summary:       conflictFindingSummary(g),
			Evidence:      evidence,
			Remediation:   "Choose one source of truth for " + name + " and update or remove the other source values.",
			RelatedChecks: uniqueStrings(g.checkIDs),
		})
	}
	return findings
}

func conflictFindingSummary(g *conflictFindingGroup) string {
	if len(g.valueSet) > 0 {
		return fmt.Sprintf("%s has %d distinct %s across %d persisted %s.",
			g.name,
			len(g.valueSet),
			plural("value", len(g.valueSet)),
			len(g.sourceSet),
			plural("source", len(g.sourceSet)),
		)
	}
	return g.name + " has shell profile issues that can change the effective value."
}

func buildIDEDriftFindings(differences []config.SideContextDifference) []core.DiagnosticFinding {
	type group struct {
		name            string
		checkIDs        []string
		sourceSet       map[string]bool
		valueSet        map[string]bool
		comparedAgainst string
		globalSource    string
	}

	groups := make(map[string]*group)
	var order []string
	for i, difference := range differences {
		g := groups[difference.Name]
		if g == nil {
			g = &group{
				name:      difference.Name,
				sourceSet: make(map[string]bool),
				valueSet:  make(map[string]bool),
			}
			groups[difference.Name] = g
			order = append(order, difference.Name)
		}
		g.checkIDs = append(g.checkIDs, ideDriftCheckID(i, difference.Name))
		g.sourceSet[difference.Context.String()] = true
		g.valueSet[difference.ContextValue.Value] = true
		if difference.ComparedAgainst != "" {
			g.comparedAgainst = difference.ComparedAgainst
		}
		if difference.Global != nil {
			g.globalSource = difference.Global.Source.String()
		}
	}

	findings := make([]core.DiagnosticFinding, 0, len(order))
	for _, name := range order {
		g := groups[name]
		comparedAgainst := g.comparedAgainst
		if comparedAgainst == "" {
			comparedAgainst = "terminal config"
		}
		evidence := []string{
			"IDE sources: " + strings.Join(sortedKeys(g.sourceSet), ", "),
			fmt.Sprintf("distinct IDE values: %d", len(g.valueSet)),
			"compared against: " + comparedAgainst,
		}
		if g.globalSource != "" {
			evidence = append(evidence, "terminal source: "+g.globalSource)
		}
		findings = append(findings, core.DiagnosticFinding{
			ID:            "ide-drift." + findingSlug(name),
			Status:        core.StatusWARN,
			Title:         "IDE: " + name + " differs from terminal",
			Summary:       fmt.Sprintf("%s differs in %d IDE %s compared with %s.", name, len(g.sourceSet), plural("source", len(g.sourceSet)), comparedAgainst),
			Evidence:      evidence,
			Remediation:   "Update Cursor/VS Code settings or remove the IDE override so " + name + " matches terminal config.",
			RelatedChecks: uniqueStrings(g.checkIDs),
		})
	}
	return findings
}

func conflictCheckID(index int, name string) string {
	return fmt.Sprintf("config-conflict.%02d.%s", index+1, name)
}

func ideDriftCheckID(index int, name string) string {
	return fmt.Sprintf("ide-config.%02d.%s", index+1, name)
}

func sideGatewayPrefix(source core.SourceLabel) string {
	switch source.Kind {
	case core.SourceProjectLocalSettings, core.SourceProjectSettings:
		return "Project gateway"
	case core.SourceCursorSettings:
		return "Cursor gateway"
	case core.SourceVSCodeSettings:
		return "VS Code gateway"
	default:
		return "Gateway"
	}
}

func contextFindingSlug(context string) string {
	if context == contextCurrent {
		return "current"
	}
	if context == contextPersisted {
		return "persisted"
	}
	return findingSlug(context)
}

func findingSlug(value string) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func shortEvidence(value string) string {
	value = strings.TrimSpace(value)
	const maxEvidenceLen = 180
	if len(value) <= maxEvidenceLen {
		return value
	}
	return value[:maxEvidenceLen-3] + "..."
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedUniqueStrings(values []string) []string {
	unique := uniqueStrings(values)
	sort.Strings(unique)
	return unique
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}

func plural(word string, count int) string {
	if count == 1 {
		return word
	}
	return word + "s"
}

func findingSeverity(status core.DiagnosticStatus) int {
	switch status {
	case core.StatusFAIL:
		return 4
	case core.StatusWARN:
		return 3
	case core.StatusSKIP:
		return 2
	case core.StatusOK:
		return 1
	default:
		return 0
	}
}
