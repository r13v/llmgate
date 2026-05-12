package wizard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/r13v/llmgate/internal/apply"
	"github.com/r13v/llmgate/internal/config"
	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/diagnose"
	"github.com/r13v/llmgate/internal/gateway"
	"github.com/r13v/llmgate/internal/redact"
	"github.com/r13v/llmgate/internal/system"
)

var (
	ErrNonInteractive  = errors.New("interactive setup wizard requires an interactive terminal")
	ErrStartupDeclined = errors.New("startup access declined")
	ErrSetupIncomplete = errors.New("setup incomplete")
)

type Options struct {
	System            system.System
	Gateway           gateway.Client
	Prompter          Prompter
	Input             io.Reader
	Output            io.Writer
	Accessible        bool
	SkipNetworkChecks bool
	CommandTimeout    time.Duration
	ApplyOptions      apply.ApplyOptions
}

type runResult struct {
	Read        config.ReadResult
	Diagnostics diagnose.Result
}

type displayOptions struct {
	HomeDir string
	GOOS    string
}

type runner struct {
	sys     system.System
	gateway gateway.Client
	prompts Prompter
	out     io.Writer
	network bool
	apply   apply.ApplyOptions
	command time.Duration
}

func Run(ctx context.Context, opts Options) error {
	r := newRunner(opts)
	if r.sys.Terminal == nil || !r.sys.Terminal.IsInteractive() {
		return ErrNonInteractive
	}

	approved, err := promptStartup(ctx, r.prompts)
	if err != nil {
		if isCancelError(err) {
			_, _ = fmt.Fprintln(r.out, "No files were read or changed.")
			return ErrStartupDeclined
		}
		return err
	}
	if !approved {
		_, _ = fmt.Fprintln(r.out, "No files were read or changed.")
		return ErrStartupDeclined
	}

	result, err := r.readAndDiagnose(ctx)
	if err != nil {
		return err
	}
	r.printDiagnosticSummary("Initial diagnostics", result.Diagnostics)

	for {
		selectedAction, err := promptAction(ctx, r.prompts, result)
		if err != nil {
			if isCancelError(err) {
				return nil
			}
			return err
		}

		switch selectedAction {
		case actionSetup:
			return r.runSetup(ctx, result)
		case actionRepair:
			return r.runRepair(ctx, result)
		case actionReview:
			_, _ = fmt.Fprint(r.out, diagnose.Render(result.Diagnostics, diagnose.RenderOptions{}))
			return nil
		case actionExit:
			_, _ = fmt.Fprintln(r.out, "No files were changed.")
			return nil
		default:
			return fmt.Errorf("unsupported wizard action %q", selectedAction)
		}
	}
}

func newRunner(opts Options) runner {
	out := opts.Output
	if out == nil {
		out = io.Discard
	}
	prompts := opts.Prompter
	if prompts == nil {
		prompts = HuhPrompter{
			In:         opts.Input,
			Output:     out,
			Accessible: opts.Accessible,
		}
	}
	client := opts.Gateway
	if client.Cache == nil {
		client.Cache = gateway.NewCache()
	}
	return runner{
		sys:     opts.System,
		gateway: client,
		prompts: prompts,
		out:     out,
		network: !opts.SkipNetworkChecks,
		apply:   opts.ApplyOptions,
		command: opts.CommandTimeout,
	}
}

func (r runner) readAndDiagnose(ctx context.Context) (runResult, error) {
	read, err := config.Read(r.sys, true)
	if err != nil {
		return runResult{}, err
	}
	diagnostics, err := diagnose.Run(ctx, r.sys, read, diagnose.Options{
		NetworkChecks:  r.network,
		Gateway:        r.gateway,
		CommandTimeout: r.command,
	})
	if err != nil {
		return runResult{}, err
	}
	return runResult{Read: read, Diagnostics: diagnostics}, nil
}

func (r runner) runSetup(ctx context.Context, result runResult) error {
	defaults := setupDefaults(result.Diagnostics.Resolution)
	display := displayOptions{HomeDir: result.Read.Paths.HomeDir, GOOS: result.Read.Paths.GOOS}
	bypassGatewayFailure := false

gatewayLoop:
	for {
		token, baseURL, err := promptCredentials(ctx, r.prompts, defaults, display)
		if err != nil {
			if isCancelError(err) {
				return nil
			}
			return err
		}
		defaults.Token = token
		defaults.TokenFound = true
		defaults.TokenSource = core.SourceLabel{Kind: core.SourceUserInput}
		defaults.BaseURL = baseURL
		defaults.BaseSource = core.SourceLabel{Kind: core.SourceUserInput}

		var modelList gateway.ModelListResult
		for {
			var err error
			modelList, err = r.gateway.ListModels(ctx, baseURL, token, gateway.RequestOptions{BypassFailedCache: bypassGatewayFailure})
			if err == nil {
				break
			}
			recovery, promptErr := promptGatewayRecovery(ctx, r.prompts, err, token, display)
			if promptErr != nil {
				if isCancelError(promptErr) {
					return nil
				}
				return promptErr
			}
			switch recovery {
			case gatewayRecoveryRetry:
				bypassGatewayFailure = true
				continue
			case gatewayRecoveryEdit:
				bypassGatewayFailure = true
				continue gatewayLoop
			case gatewayRecoveryExit:
				return nil
			default:
				return fmt.Errorf("unsupported gateway recovery %q", recovery)
			}
		}
		_, _ = fmt.Fprintf(r.out, "Gateway validation passed: %s\n", modelList.Summary)

		modelDefaults := modelDefaultsFromResolution(result.Diagnostics.Resolution, token, baseURL)
		for {
			values, modelErr := r.chooseModels(ctx, modelList.Models, modelDefaults, display)
			if modelErr != nil {
				if isCancelError(modelErr) {
					return nil
				}
				return modelErr
			}
			unavailable := unavailableModels(values, modelList.Models)
			if len(unavailable) > 0 {
				recovery, promptErr := promptModelRecovery(ctx, r.prompts, "Selected model unavailable", "Unavailable models: "+strings.Join(unavailable, ", "), token, display)
				if promptErr != nil {
					if isCancelError(promptErr) {
						return nil
					}
					return promptErr
				}
				switch recovery {
				case modelRecoveryChoose:
					modelDefaults = values
					continue
				case modelRecoveryEdit:
					defaults.Token = token
					defaults.BaseURL = baseURL
					bypassGatewayFailure = true
					continue gatewayLoop
				case modelRecoveryExit:
					return nil
				}
			}
			if err := r.validateSelectedModels(ctx, values); err != nil {
				recovery, promptErr := promptModelRecovery(ctx, r.prompts, "Model probe failed", err.Error(), token, display)
				if promptErr != nil {
					if isCancelError(promptErr) {
						return nil
					}
					return promptErr
				}
				switch recovery {
				case modelRecoveryChoose:
					modelDefaults = values
					continue
				case modelRecoveryEdit:
					defaults.Token = token
					defaults.BaseURL = baseURL
					bypassGatewayFailure = true
					continue gatewayLoop
				case modelRecoveryExit:
					return nil
				default:
					return fmt.Errorf("unsupported model recovery %q", recovery)
				}
			}
			_, _ = fmt.Fprintln(r.out, "Model probes passed.")
			return r.selectTargetsAndApplySetup(ctx, result.Read.Paths, result.Read.WriteTargets, values)
		}
	}
}

func (r runner) chooseModels(ctx context.Context, models []string, defaults core.SetupValues, display displayOptions) (core.SetupValues, error) {
	recommendation, ok := gateway.Recommend(models)
	if ok {
		useRecommendation, err := promptUseRecommendation(ctx, r.prompts, recommendation, defaults.AuthToken, display)
		if err != nil {
			return core.SetupValues{}, err
		}
		if useRecommendation {
			return recommendation.SetupValues(defaults.AuthToken, defaults.BaseURL), nil
		}
	}
	return promptManualModels(ctx, r.prompts, models, defaults, recommendation, display)
}

func (r runner) validateSelectedModels(ctx context.Context, values core.SetupValues) error {
	for _, model := range uniqueModels(values) {
		if _, err := r.gateway.ProbeModel(ctx, values.BaseURL, values.AuthToken, model, gateway.RequestOptions{BypassFailedCache: true}); err != nil {
			return err
		}
	}
	return nil
}

func (r runner) selectTargetsAndApplySetup(ctx context.Context, paths system.DiscoveredPaths, targets []core.WriteTarget, values core.SetupValues) error {
	display := displayOptions{HomeDir: paths.HomeDir, GOOS: paths.GOOS}
	manualTargets := filterTargets(targets, false)
	for {
		selectedTargets, err := promptTargets(ctx, r.prompts, targets, display)
		if err != nil {
			if isCancelError(err) {
				return nil
			}
			return err
		}
		if len(selectedTargets) == 0 {
			_, _ = fmt.Fprintln(r.out, "No write targets selected. Nothing was changed.")
			return nil
		}

		planTargets := append([]core.WriteTarget(nil), selectedTargets...)
		planTargets = append(planTargets, manualTargets...)
		plan, err := apply.BuildSetupPlan(r.sys, paths, planTargets, values)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprint(r.out, apply.RenderPlan(plan, apply.RenderOptions{KnownSecrets: []string{values.AuthToken}}))

		approved, err := promptApply(ctx, r.prompts)
		if err != nil {
			if isCancelError(err) {
				return nil
			}
			return err
		}
		if !approved {
			continue
		}

		return r.applyPlanAndFinalize(ctx, plan, []string{values.AuthToken})
	}
}

func (r runner) runRepair(ctx context.Context, result runResult) error {
	if !result.Diagnostics.HasRepairableStaleShellModelWarnings() {
		_, _ = fmt.Fprintln(r.out, "No repairable warnings found.")
		return nil
	}
	plan, err := apply.BuildRepairPlan(r.sys, result.Read.Paths, result.Diagnostics.RepairableStaleShellModelWarnings)
	if err != nil {
		return err
	}
	if len(plan.Targets) == 0 {
		_, _ = fmt.Fprintln(r.out, "No repairable warnings found.")
		return nil
	}
	_, _ = fmt.Fprint(r.out, apply.RenderPlan(plan, apply.RenderOptions{}))
	approved, err := promptApply(ctx, r.prompts)
	if err != nil {
		if isCancelError(err) {
			return nil
		}
		return err
	}
	if !approved {
		_, _ = fmt.Fprintln(r.out, "No files were changed.")
		return nil
	}
	return r.applyPlanAndFinalize(ctx, plan, nil)
}

func (r runner) applyPlanAndFinalize(ctx context.Context, plan apply.Plan, knownSecrets []string) error {
	result, err := apply.Apply(r.sys, plan, r.apply)
	_, _ = fmt.Fprint(r.out, apply.RenderResult(result, apply.RenderOptions{
		KnownSecrets: knownSecrets,
		HomeDir:      plan.HomeDir,
		GOOS:         plan.GOOS,
	}))
	if err != nil {
		return err
	}

	final, err := r.readAndDiagnose(ctx)
	if err != nil {
		return err
	}
	r.printDiagnosticSummary("Final diagnostics", final.Diagnostics)
	switch final.Diagnostics.Status() {
	case core.StatusFAIL:
		_, _ = fmt.Fprintln(r.out, "Setup incomplete")
		_, _ = fmt.Fprint(r.out, diagnose.Render(final.Diagnostics, diagnose.RenderOptions{}))
		return ErrSetupIncomplete
	case core.StatusOK:
		_, _ = fmt.Fprintln(r.out, "Configured")
	default:
		_, _ = fmt.Fprintln(r.out, "Configured with warnings")
	}
	_, _ = fmt.Fprintln(r.out, "Restart your terminal and IDE for changes to take effect.")
	return nil
}

func (r runner) printDiagnosticSummary(title string, result diagnose.Result) {
	display := displayOptions{HomeDir: result.Read.Paths.HomeDir, GOOS: result.Read.Paths.GOOS}
	knownSecrets := diagnose.KnownSecrets(result)
	_, _ = fmt.Fprintf(r.out, "%s: %s\n", title, result.Status())
	for _, section := range result.Sections {
		for _, check := range section.Checks {
			if check.Status != core.StatusWARN && check.Status != core.StatusFAIL {
				continue
			}
			summary := check.Summary
			if summary == "" {
				summary = check.Title
			}
			summary = sanitizeText(summary, knownSecrets, display)
			_, _ = fmt.Fprintf(r.out, "- %s: %s\n", check.Status, summary)
		}
	}
}

func setupDefaults(resolution config.Resolution) credentialDefaults {
	defaults := credentialDefaults{}
	if value, ok := preferredResolved(core.VarAnthropicAuthToken, resolution); ok {
		defaults.Token = value.Value
		defaults.TokenFound = true
		defaults.TokenSource = value.Source
	}
	if value, ok := preferredResolved(core.VarAnthropicBaseURL, resolution); ok {
		defaults.BaseURL = value.Value
		defaults.BaseSource = value.Source
	}
	return defaults
}

func modelDefaultsFromResolution(resolution config.Resolution, token, baseURL string) core.SetupValues {
	values := core.SetupValues{AuthToken: token, BaseURL: baseURL}
	if value, ok := preferredResolved(core.VarAnthropicModel, resolution); ok {
		values.Model = value.Value
	}
	if value, ok := preferredResolved(core.VarAnthropicDefaultHaikuModel, resolution); ok {
		values.HaikuModel = value.Value
	}
	if value, ok := preferredResolved(core.VarAnthropicDefaultSonnetModel, resolution); ok {
		values.SonnetModel = value.Value
	}
	if value, ok := preferredResolved(core.VarAnthropicDefaultOpusModel, resolution); ok {
		values.OpusModel = value.Value
	}
	return values
}

func preferredResolved(name string, resolution config.Resolution) (core.ResolvedValue, bool) {
	if value, ok := resolution.Current.Get(name); ok {
		return value, true
	}
	return resolution.Persisted.Get(name)
}

func unavailableModels(values core.SetupValues, available []string) []string {
	availableSet := make(map[string]bool, len(available))
	for _, model := range available {
		availableSet[model] = true
	}
	var unavailable []string
	for _, model := range uniqueModels(values) {
		if !availableSet[model] {
			unavailable = append(unavailable, model)
		}
	}
	sort.Strings(unavailable)
	return unavailable
}

func uniqueModels(values core.SetupValues) []string {
	seen := map[string]bool{}
	for _, model := range []string{values.Model, values.HaikuModel, values.SonnetModel, values.OpusModel} {
		if model != "" {
			seen[model] = true
		}
	}
	models := make([]string, 0, len(seen))
	for model := range seen {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

func filterTargets(targets []core.WriteTarget, writable bool) []core.WriteTarget {
	out := make([]core.WriteTarget, 0)
	for _, target := range targets {
		if target.Writable == writable {
			out = append(out, target)
		}
	}
	return out
}

func sourceLabel(label core.SourceLabel, display displayOptions) string {
	return sanitizeText(label.String(), nil, display)
}

func sanitizeText(value string, knownSecrets []string, display displayOptions) string {
	return redact.Text(value, redact.Options{
		KnownSecrets: knownSecrets,
		HomeDir:      display.HomeDir,
		GOOS:         display.GOOS,
	})
}
