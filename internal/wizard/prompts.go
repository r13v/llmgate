package wizard

import (
	"context"
	"fmt"
	"strings"

	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/gateway"
	"github.com/r13v/llmgate/internal/redact"
)

type action string

const (
	actionSetup  action = "setup"
	actionRepair action = "repair"
	actionReview action = "review"
	actionExit   action = "exit"
)

type gatewayRecovery string

const (
	gatewayRecoveryEdit  gatewayRecovery = "edit"
	gatewayRecoveryRetry gatewayRecovery = "retry"
	gatewayRecoveryExit  gatewayRecovery = "exit"
)

type modelRecovery string

const (
	modelRecoveryChoose modelRecovery = "choose"
	modelRecoveryEdit   modelRecovery = "edit"
	modelRecoveryExit   modelRecovery = "exit"
)

type credentialDefaults struct {
	Token       string
	TokenFound  bool
	TokenSource core.SourceLabel
	BaseURL     string
	BaseSource  core.SourceLabel
}

func promptStartup(ctx context.Context, prompts Prompter) (bool, error) {
	return prompts.Confirm(ctx, ConfirmPrompt{
		Title: "Allow llmgate to inspect local Claude Code configuration?",
		Description: strings.Join([]string{
			"Reads ~/.claude/settings.json, supported shell or Windows user environment, current process environment, VS Code and Cursor settings when present, and project .claude settings.",
			"Runs claude --version. If existing gateway credentials are found, diagnostics may list models and send tiny ping probes.",
			"No files or user environment variables are changed without separate apply plan approval.",
		}, "\n"),
		Affirmative: "Allow",
		Negative:    "Decline",
		Default:     false,
	})
}

func promptAction(ctx context.Context, prompts Prompter, result runResult) (action, error) {
	options := []Option{
		{Label: "Setup", Value: string(actionSetup)},
	}
	if result.Diagnostics.HasRepairableStaleShellModelWarnings() {
		options = append(options, Option{Label: "Repair warnings", Value: string(actionRepair)})
	}
	options = append(options,
		Option{Label: "Review details", Value: string(actionReview)},
		Option{Label: "Exit", Value: string(actionExit)},
	)

	defaultAction := string(actionSetup)
	if result.Diagnostics.Status() == core.StatusOK {
		defaultAction = string(actionExit)
	}
	value, err := prompts.Select(ctx, SelectPrompt{
		Title:       fmt.Sprintf("llmgate diagnosis: %s", result.Diagnostics.Status()),
		Description: "Choose what to do next.",
		Options:     options,
		Default:     defaultAction,
	})
	return action(value), err
}

func promptCredentials(ctx context.Context, prompts Prompter, defaults credentialDefaults, display displayOptions) (string, string, error) {
	token := defaults.Token
	if defaults.TokenFound {
		description := "Existing gateway token found in " + sourceLabel(defaults.TokenSource, display) + "."
		if defaults.BaseURL != "" {
			description += "\nBase URL default: " + displayBaseURLDefault(defaults.BaseURL, []string{token}, display)
			if defaults.BaseSource.Kind != core.SourceUnknown {
				description += " from " + sourceLabel(defaults.BaseSource, display)
			}
		}
		reuse, err := prompts.Confirm(ctx, ConfirmPrompt{
			Title:       "Use existing gateway token?",
			Description: description,
			Affirmative: "Reuse",
			Negative:    "Replace",
			Default:     true,
		})
		if err != nil {
			return "", "", err
		}
		if !reuse {
			token = ""
		}
	}

	if token == "" {
		value, err := prompts.Input(ctx, InputPrompt{
			Title:       "Gateway API token",
			Description: "Stored as ANTHROPIC_AUTH_TOKEN if you approve the apply plan.",
			Required:    true,
			Secret:      true,
		})
		if err != nil {
			return "", "", err
		}
		token = value
	}

	baseURLDefault := defaults.BaseURL
	if baseURLDefault != "" {
		baseURLDefault = safeBaseURLDefault(baseURLDefault, []string{token}, display)
	}
	baseURLPlaceholder := ""
	if baseURLDefault == "" {
		baseURLPlaceholder = core.DefaultBaseURLPlaceholder
	}
	baseURL, err := prompts.Input(ctx, InputPrompt{
		Title:       "LiteLLM gateway base URL",
		Description: "Use the gateway root URL. llmgate will validate /v1/models or /models fallback.",
		Placeholder: baseURLPlaceholder,
		Default:     baseURLDefault,
		Required:    true,
	})
	if err != nil {
		return "", "", err
	}
	return token, baseURL, nil
}

func displayBaseURLDefault(raw string, knownSecrets []string, display displayOptions) string {
	if canonical, err := gateway.NormalizeBaseURL(raw); err == nil {
		return sanitizeText(canonical, knownSecrets, display)
	}
	return sanitizeText(raw, knownSecrets, display)
}

func safeBaseURLDefault(raw string, knownSecrets []string, display displayOptions) string {
	canonical, err := gateway.NormalizeBaseURL(raw)
	if err != nil {
		return ""
	}
	sanitized := sanitizeText(canonical, knownSecrets, display)
	if sanitized != canonical {
		return ""
	}
	return sanitized
}

func promptGatewayRecovery(ctx context.Context, prompts Prompter, err error, token string, display displayOptions) (gatewayRecovery, error) {
	value, promptErr := prompts.Select(ctx, SelectPrompt{
		Title:       "Gateway validation failed",
		Description: sanitizeText(err.Error(), []string{token}, display),
		Options: []Option{
			{Label: "Edit token/base URL", Value: string(gatewayRecoveryEdit)},
			{Label: "Retry", Value: string(gatewayRecoveryRetry)},
			{Label: "Exit", Value: string(gatewayRecoveryExit)},
		},
		Default: string(gatewayRecoveryEdit),
	})
	return gatewayRecovery(value), promptErr
}

func promptUseRecommendation(ctx context.Context, prompts Prompter, recommendation gateway.Recommendation, token string, display displayOptions) (bool, error) {
	return prompts.Confirm(ctx, ConfirmPrompt{
		Title: "Use recommended Claude model mapping?",
		Description: strings.Join([]string{
			"Primary: " + sanitizeText(recommendation.Primary, []string{token}, display),
			"Haiku: " + sanitizeText(recommendation.Haiku, []string{token}, display),
			"Sonnet: " + sanitizeText(recommendation.Sonnet, []string{token}, display),
			"Opus: " + sanitizeText(recommendation.Opus, []string{token}, display),
		}, "\n"),
		Affirmative: "Use",
		Negative:    "Choose manually",
		Default:     true,
	})
}

func promptManualModels(ctx context.Context, prompts Prompter, models []string, defaults core.SetupValues, recommendation gateway.Recommendation, display displayOptions) (core.SetupValues, error) {
	knownSecrets := []string{defaults.AuthToken}
	primaryDefault := firstAvailable(models, defaults.Model, recommendation.Primary)
	primary, err := prompts.Select(ctx, SelectPrompt{
		Title:   "Primary Claude Code model",
		Options: selectOptions(models, knownSecrets, display),
		Default: primaryDefault,
	})
	if err != nil {
		return core.SetupValues{}, err
	}

	advanced, err := prompts.Confirm(ctx, ConfirmPrompt{
		Title:       "Set advanced Haiku, Sonnet, and Opus model overrides?",
		Description: "Declining uses the primary model for all Claude Code model tiers.",
		Affirmative: "Set overrides",
		Negative:    "Use primary",
		Default:     false,
	})
	if err != nil {
		return core.SetupValues{}, err
	}
	values := core.SetupValues{
		AuthToken:   defaults.AuthToken,
		BaseURL:     defaults.BaseURL,
		Model:       primary,
		HaikuModel:  primary,
		SonnetModel: primary,
		OpusModel:   primary,
	}
	if !advanced {
		return values, nil
	}

	haiku, err := promptTierModel(ctx, prompts, "Haiku tier model", models, firstAvailable(models, defaults.HaikuModel, recommendation.Haiku, primary), knownSecrets, display)
	if err != nil {
		return core.SetupValues{}, err
	}
	sonnet, err := promptTierModel(ctx, prompts, "Sonnet tier model", models, firstAvailable(models, defaults.SonnetModel, recommendation.Sonnet, primary), knownSecrets, display)
	if err != nil {
		return core.SetupValues{}, err
	}
	opus, err := promptTierModel(ctx, prompts, "Opus tier model", models, firstAvailable(models, defaults.OpusModel, recommendation.Opus, primary), knownSecrets, display)
	if err != nil {
		return core.SetupValues{}, err
	}
	values.HaikuModel = haiku
	values.SonnetModel = sonnet
	values.OpusModel = opus
	return values, nil
}

func promptTierModel(ctx context.Context, prompts Prompter, title string, models []string, defaultValue string, knownSecrets []string, display displayOptions) (string, error) {
	return prompts.Select(ctx, SelectPrompt{
		Title:   title,
		Options: selectOptions(models, knownSecrets, display),
		Default: defaultValue,
	})
}

func promptModelRecovery(ctx context.Context, prompts Prompter, title, detail, token string, display displayOptions) (modelRecovery, error) {
	value, err := prompts.Select(ctx, SelectPrompt{
		Title:       title,
		Description: sanitizeText(detail, []string{token}, display),
		Options: []Option{
			{Label: "Choose models", Value: string(modelRecoveryChoose)},
			{Label: "Edit token/base URL", Value: string(modelRecoveryEdit)},
			{Label: "Exit", Value: string(modelRecoveryExit)},
		},
		Default: string(modelRecoveryChoose),
	})
	return modelRecovery(value), err
}

func promptTargets(ctx context.Context, prompts Prompter, targets []core.WriteTarget, display displayOptions) ([]core.WriteTarget, error) {
	options := make([]Option, 0, len(targets))
	indexByValue := make(map[string]int)
	for index, target := range targets {
		if !target.Writable {
			continue
		}
		value := numberedValue(index)
		indexByValue[value] = index
		label := target.Title + " - " + targetLocation(target, display)
		options = append(options, Option{Label: label, Value: value, Selected: true})
	}
	selected, err := prompts.MultiSelect(ctx, MultiSelectPrompt{
		Title:       "Select write targets",
		Description: "Writable targets are selected by default. Manual targets are shown in the apply plan when relevant.",
		Options:     options,
	})
	if err != nil {
		return nil, err
	}

	out := make([]core.WriteTarget, 0, len(selected))
	for _, value := range selected {
		index, ok := indexByValue[value]
		if !ok {
			continue
		}
		out = append(out, targets[index])
	}
	return out, nil
}

func promptApply(ctx context.Context, prompts Prompter) (bool, error) {
	return prompts.Confirm(ctx, ConfirmPrompt{
		Title:       "Apply these changes?",
		Description: "No files or user environment variables are changed unless you approve.",
		Affirmative: "Apply",
		Negative:    "Back",
		Default:     false,
	})
}

func firstAvailable(models []string, candidates ...string) string {
	modelSet := make(map[string]bool, len(models))
	for _, model := range models {
		modelSet[model] = true
	}
	for _, candidate := range candidates {
		if candidate != "" && modelSet[candidate] {
			return candidate
		}
	}
	if len(models) > 0 {
		return models[0]
	}
	return ""
}

func targetLocation(target core.WriteTarget, display displayOptions) string {
	if target.Path == "" {
		return "user environment"
	}
	return redact.ShortenHomePath(target.Path, display.HomeDir, display.GOOS)
}
