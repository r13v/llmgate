package core

const (
	VarAnthropicAuthToken                   = "ANTHROPIC_AUTH_TOKEN"
	VarAnthropicBaseURL                     = "ANTHROPIC_BASE_URL"
	VarAnthropicModel                       = "ANTHROPIC_MODEL"
	VarAnthropicDefaultHaikuModel           = "ANTHROPIC_DEFAULT_HAIKU_MODEL"
	VarAnthropicDefaultSonnetModel          = "ANTHROPIC_DEFAULT_SONNET_MODEL"
	VarAnthropicDefaultOpusModel            = "ANTHROPIC_DEFAULT_OPUS_MODEL"
	VarClaudeCodeEnableTelemetry            = "CLAUDE_CODE_ENABLE_TELEMETRY"
	VarClaudeCodeDisableNonessentialTraffic = "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"
	VarOTELMetricsExporter                  = "OTEL_METRICS_EXPORTER"
	VarAnthropicDisableNonessentialTraffic  = "ANTHROPIC_DISABLE_NONESSENTIAL_TRAFFIC"
	VarDisablePromptCachingHaiku            = "DISABLE_PROMPT_CACHING_HAIKU"
	VarDisablePromptCachingSonnet           = "DISABLE_PROMPT_CACHING_SONNET"
	VarDisablePromptCachingOpus             = "DISABLE_PROMPT_CACHING_OPUS"
	VarClaudeCodeDisableExperimentalBetas   = "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS"
	DefaultBaseURLPlaceholder               = "https://your-litellm-gateway.example.com"
)

type ManagedValue struct {
	Name            string
	Meaning         string
	Required        bool
	Secret          bool
	Default         string
	BehaviorDefault bool
}

var RequiredValues = []ManagedValue{
	{
		Name:     VarAnthropicAuthToken,
		Meaning:  "Gateway API token",
		Required: true,
		Secret:   true,
	},
	{
		Name:     VarAnthropicBaseURL,
		Meaning:  "LiteLLM-compatible gateway base URL",
		Required: true,
	},
	{
		Name:     VarAnthropicModel,
		Meaning:  "Primary Claude Code model",
		Required: true,
	},
	{
		Name:     VarAnthropicDefaultHaikuModel,
		Meaning:  "Haiku tier model",
		Required: true,
	},
	{
		Name:     VarAnthropicDefaultSonnetModel,
		Meaning:  "Sonnet tier model",
		Required: true,
	},
	{
		Name:     VarAnthropicDefaultOpusModel,
		Meaning:  "Opus tier model",
		Required: true,
	},
}

var BehaviorPrivacyDefaults = []ManagedValue{
	{
		Name:            VarClaudeCodeEnableTelemetry,
		Meaning:         "disable Claude Code telemetry",
		Default:         "0",
		BehaviorDefault: true,
	},
	{
		Name:            VarClaudeCodeDisableNonessentialTraffic,
		Meaning:         "reduce Claude Code optional network traffic",
		Default:         "1",
		BehaviorDefault: true,
	},
	{
		Name:            VarOTELMetricsExporter,
		Meaning:         "keep telemetry exporter explicit",
		Default:         "otlp",
		BehaviorDefault: true,
	},
	{
		Name:            VarAnthropicDisableNonessentialTraffic,
		Meaning:         "reduce optional Anthropic traffic",
		Default:         "1",
		BehaviorDefault: true,
	},
	{
		Name:            VarDisablePromptCachingHaiku,
		Meaning:         "disable LiteLLM prompt caching for Haiku tier",
		Default:         "1",
		BehaviorDefault: true,
	},
	{
		Name:            VarDisablePromptCachingSonnet,
		Meaning:         "disable LiteLLM prompt caching for Sonnet tier",
		Default:         "1",
		BehaviorDefault: true,
	},
	{
		Name:            VarDisablePromptCachingOpus,
		Meaning:         "disable LiteLLM prompt caching for Opus tier",
		Default:         "1",
		BehaviorDefault: true,
	},
	{
		Name:            VarClaudeCodeDisableExperimentalBetas,
		Meaning:         "disable Claude Code experimental beta headers",
		Default:         "1",
		BehaviorDefault: true,
	},
}

var managedByName = func() map[string]ManagedValue {
	values := make(map[string]ManagedValue, len(RequiredValues)+len(BehaviorPrivacyDefaults))
	for _, value := range RequiredValues {
		values[value.Name] = value
	}
	for _, value := range BehaviorPrivacyDefaults {
		values[value.Name] = value
	}
	return values
}()

func AllManagedValues() []ManagedValue {
	values := make([]ManagedValue, 0, len(RequiredValues)+len(BehaviorPrivacyDefaults))
	values = append(values, RequiredValues...)
	values = append(values, BehaviorPrivacyDefaults...)
	return values
}

func AllManagedNames() []string {
	values := AllManagedValues()
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.Name)
	}
	return names
}

func FindManagedValue(name string) (ManagedValue, bool) {
	value, ok := managedByName[name]
	return value, ok
}

func IsManaged(name string) bool {
	_, ok := managedByName[name]
	return ok
}

func IsSecret(name string) bool {
	value, ok := managedByName[name]
	return ok && value.Secret
}
