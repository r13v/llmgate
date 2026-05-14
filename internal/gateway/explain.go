package gateway

import (
	"errors"
	"fmt"
	"strings"
)

type FailureExplanation struct {
	Cause       string
	Evidence    []string
	Remediation string
}

func ExplainFailure(err error) FailureExplanation {
	if err == nil {
		return FailureExplanation{}
	}

	var gatewayErr *Error
	if !errors.As(err, &gatewayErr) {
		return FailureExplanation{
			Cause:       defaultFailureCause(),
			Evidence:    explanationEvidence("reason", err.Error()),
			Remediation: defaultFailureRemediation(),
		}
	}

	explanation := FailureExplanation{
		Cause:       failureCause(gatewayErr.Kind),
		Evidence:    failureEvidence(gatewayErr),
		Remediation: failureRemediation(gatewayErr.Kind),
	}
	if explanation.Cause == "" {
		explanation.Cause = defaultFailureCause()
	}
	if explanation.Remediation == "" {
		explanation.Remediation = defaultFailureRemediation()
	}
	return explanation
}

func failureEvidence(err *Error) []string {
	if err == nil {
		return nil
	}

	var evidence []string
	if err.Operation != "" {
		evidence = append(evidence, "operation: "+err.Operation)
	}
	if err.URL != "" {
		evidence = append(evidence, "request URL: "+err.URL)
	}
	if err.Kind != "" {
		evidence = append(evidence, "failure kind: "+string(err.Kind))
	}
	if err.StatusCode != 0 {
		evidence = append(evidence, fmt.Sprintf("HTTP status: %d", err.StatusCode))
	}
	if err.Detail != "" {
		evidence = append(evidence, explanationEvidence("gateway message", err.Detail)...)
	} else if err.Err != nil {
		evidence = append(evidence, explanationEvidence("gateway message", err.Err.Error())...)
	}
	if err.Cached {
		evidence = append(evidence, "cached failure: true")
	}
	return evidence
}

func failureCause(kind FailureKind) string {
	switch kind {
	case FailureAuth:
		return "The gateway rejected the configured ANTHROPIC_AUTH_TOKEN."
	case FailureNetwork:
		return "llmgate could not reach the configured gateway."
	case FailureInvalidURL:
		return "ANTHROPIC_BASE_URL is not a valid gateway URL."
	case FailureInvalidJSON:
		return "The gateway response was not OpenAI-compatible JSON."
	case FailureEmptyModels:
		return "The gateway returned no usable model IDs."
	case FailureHTTP:
		return "The gateway returned a non-success HTTP response."
	default:
		return ""
	}
}

func failureRemediation(kind FailureKind) string {
	switch kind {
	case FailureAuth:
		return "Update ANTHROPIC_AUTH_TOKEN for the active source, or remove the stale override."
	case FailureNetwork:
		return "Check ANTHROPIC_BASE_URL, DNS, VPN/proxy, TLS, and network access."
	case FailureInvalidURL:
		return "Set ANTHROPIC_BASE_URL to an http(s) LiteLLM gateway root or /v1 URL."
	case FailureInvalidJSON:
		return "Inspect the gateway response and OpenAI-compatible model-list route."
	case FailureEmptyModels:
		return "Configure the gateway to expose at least one usable model ID."
	case FailureHTTP:
		return "Inspect the gateway/upstream logs, base URL, and selected model routing."
	default:
		return ""
	}
}

func defaultFailureCause() string {
	return "Gateway validation failed before llmgate could verify the configuration."
}

func defaultFailureRemediation() string {
	return "Inspect the gateway error, update the active gateway configuration, and rerun diagnostics."
}

func explanationEvidence(prefix, value string) []string {
	value = conciseExplanationDetail(value)
	if value == "" {
		return nil
	}
	return []string{prefix + ": " + value}
}

func conciseExplanationDetail(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if containsNoisyLiteLLMDetail(value) {
		return "gateway returned LiteLLM token verification diagnostics; review details for the full response"
	}
	return value
}

func containsNoisyLiteLLMDetail(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "litellm_verificationtokentable") ||
		strings.Contains(lower, "key hash")
}
