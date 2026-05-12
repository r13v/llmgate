package apply

import (
	"fmt"
	"strings"

	"github.com/r13v/llmgate/internal/redact"
)

type RenderOptions struct {
	KnownSecrets []string
	HomeDir      string
	GOOS         string
}

func RenderPlan(plan Plan, opts RenderOptions) string {
	opts = renderOptionsWithPlanDefaults(plan, opts)
	secrets := append([]string(nil), opts.KnownSecrets...)
	secrets = append(secrets, planSecrets(plan)...)

	var builder strings.Builder
	switch plan.Purpose {
	case PurposeRepair:
		builder.WriteString("Apply plan: repair stale shell model assignments\n")
	default:
		builder.WriteString("Apply plan: setup gateway credentials, model mapping, and privacy/traffic defaults\n")
	}
	if len(plan.Targets) == 0 {
		builder.WriteString("changes: none\n")
	}
	for _, target := range plan.Targets {
		writeTargetPlan(&builder, target, opts)
	}
	return redact.Text(builder.String(), redact.Options{
		KnownSecrets: secrets,
		HomeDir:      opts.HomeDir,
		GOOS:         opts.GOOS,
	})
}

func RenderResult(result Result, opts RenderOptions) string {
	secrets := append([]string(nil), opts.KnownSecrets...)
	secrets = append(secrets, resultSecrets(result)...)

	var builder strings.Builder
	builder.WriteString("Write results\n")
	if len(result.Targets) == 0 {
		builder.WriteString("- no targets\n")
	}
	for _, target := range result.Targets {
		location := target.Target.Path
		if location == "" {
			location = "user environment"
		}
		location = redact.ShortenHomePath(location, opts.HomeDir, opts.GOOS)
		_, _ = fmt.Fprintf(&builder, "\n%s\n", target.Target.Title)
		_, _ = fmt.Fprintf(&builder, "location: %s\n", location)
		_, _ = fmt.Fprintf(&builder, "operation: %s\n", target.Operation)
		_, _ = fmt.Fprintf(&builder, "status: %s\n", target.Status)
		_, _ = fmt.Fprintf(&builder, "sensitive: %t\n", target.Sensitive)
		if target.BackupPath != "" {
			_, _ = fmt.Fprintf(&builder, "backup: %s\n", redact.ShortenHomePath(target.BackupPath, opts.HomeDir, opts.GOOS))
		}
		if len(target.Changes) == 0 {
			builder.WriteString("changes: none\n")
		} else {
			builder.WriteString("changes:\n")
			for _, change := range target.Changes {
				_, _ = fmt.Fprintf(&builder, "- %s: %s -> %s\n", change.Name, displayValueState(change.Old, change.Secret), displayValueState(change.New, change.Secret))
			}
		}
		for _, warning := range target.Warnings {
			_, _ = fmt.Fprintf(&builder, "warning: %s\n", warning)
		}
		if target.Error != "" {
			_, _ = fmt.Fprintf(&builder, "error: %s\n", target.Error)
		}
	}
	return redact.Text(builder.String(), redact.Options{
		KnownSecrets: secrets,
		HomeDir:      opts.HomeDir,
		GOOS:         opts.GOOS,
	})
}

func writeTargetPlan(builder *strings.Builder, target TargetPlan, opts RenderOptions) {
	location := target.Target.Path
	if location == "" {
		location = "user environment"
	}
	location = redact.ShortenHomePath(location, opts.HomeDir, opts.GOOS)

	_, _ = fmt.Fprintf(builder, "\n%s\n", target.Target.Title)
	_, _ = fmt.Fprintf(builder, "location: %s\n", location)
	_, _ = fmt.Fprintf(builder, "operation: %s\n", target.Operation)
	_, _ = fmt.Fprintf(builder, "sensitive: %t\n", target.Sensitive)
	if target.Operation == OperationUpdateFile {
		builder.WriteString("backup: .llmgate.bak path will be reported after writing\n")
	}
	if len(target.Changes) == 0 {
		builder.WriteString("changes: none\n")
	} else {
		builder.WriteString("changes:\n")
		for _, change := range target.Changes {
			_, _ = fmt.Fprintf(builder, "- %s: %s -> %s\n", change.Name, displayValueState(change.Old, change.Secret), displayValueState(change.New, change.Secret))
		}
	}
	for _, warning := range target.Warnings {
		_, _ = fmt.Fprintf(builder, "warning: %s\n", warning)
	}
	if len(target.ManualLines) > 0 {
		builder.WriteString("manual lines:\n")
		for _, line := range target.ManualLines {
			_, _ = fmt.Fprintf(builder, "- %s\n", line)
		}
	}
}

func displayValueState(value ValueState, secret bool) string {
	if !value.Set {
		return "<unset>"
	}
	if value.Value == "" {
		return "<empty>"
	}
	if secret {
		return redact.MaskSecret(value.Value)
	}
	return value.Value
}

func renderOptionsWithPlanDefaults(plan Plan, opts RenderOptions) RenderOptions {
	if opts.HomeDir == "" {
		opts.HomeDir = plan.HomeDir
	}
	if opts.GOOS == "" {
		opts.GOOS = plan.GOOS
	}
	return opts
}

func planSecrets(plan Plan) []string {
	var secrets []string
	for _, target := range plan.Targets {
		for _, change := range target.Changes {
			if !change.Secret {
				continue
			}
			if change.Old.Set {
				secrets = append(secrets, change.Old.Value)
			}
			if change.New.Set {
				secrets = append(secrets, change.New.Value)
			}
		}
	}
	return secrets
}

func resultSecrets(result Result) []string {
	var secrets []string
	for _, target := range result.Targets {
		for _, change := range target.Changes {
			if !change.Secret {
				continue
			}
			if change.Old.Set {
				secrets = append(secrets, change.Old.Value)
			}
			if change.New.Set {
				secrets = append(secrets, change.New.Value)
			}
		}
	}
	return secrets
}
