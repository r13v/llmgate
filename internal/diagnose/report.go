package diagnose

import (
	"fmt"
	"strings"

	"github.com/r13v/llmgate/internal/config"
	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/redact"
)

type RenderOptions struct {
	KnownSecrets []string
	HomeDir      string
	GOOS         string
}

func Render(result Result, opts RenderOptions) string {
	if opts.HomeDir == "" {
		opts.HomeDir = result.Read.Paths.HomeDir
	}
	if opts.GOOS == "" {
		opts.GOOS = result.Read.Paths.GOOS
	}
	opts.KnownSecrets = append(opts.KnownSecrets, KnownSecrets(result)...)

	var builder strings.Builder
	_, _ = fmt.Fprintf(&builder, "llmgate diagnosis: %s\n", result.Status())
	for i, section := range result.Sections {
		if i == 0 {
			builder.WriteByte('\n')
		} else {
			builder.WriteByte('\n')
		}
		_, _ = fmt.Fprintf(&builder, "*%s*\n", section.Title)
		if len(section.Checks) == 0 {
			builder.WriteString("- OK: no checks\n")
			continue
		}
		for _, check := range section.Checks {
			summary := check.Summary
			if summary == "" {
				summary = check.Title
			}
			_, _ = fmt.Fprintf(&builder, "- %s: %s\n", check.Status, summary)
			for _, detail := range check.Details {
				if detail == "" {
					continue
				}
				_, _ = fmt.Fprintf(&builder, "  - %s\n", detail)
			}
		}
	}

	output := builder.String()
	return redact.Text(output, redact.Options{
		KnownSecrets: opts.KnownSecrets,
		HomeDir:      opts.HomeDir,
		GOOS:         opts.GOOS,
	})
}

// KnownSecrets returns secret values discovered in the diagnostic input.
func KnownSecrets(result Result) []string {
	var secrets []string
	secrets = append(secrets, secretsFromResolved(result.Resolution.Current)...)
	secrets = append(secrets, secretsFromResolved(result.Resolution.Persisted)...)
	for _, source := range result.Read.Sources {
		secrets = append(secrets, secretsFromSource(source)...)
	}
	return uniqueSecrets(secrets)
}

func secretsFromResolved(resolved core.ResolvedConfig) []string {
	secrets := make([]string, 0)
	for _, value := range resolved.Values {
		if value.Secret && value.Value != "" {
			secrets = append(secrets, value.Value)
		}
		for _, shadowed := range value.Shadowed {
			if shadowed.Secret && shadowed.Value != "" {
				secrets = append(secrets, shadowed.Value)
			}
		}
	}
	return secrets
}

func secretsFromSource(source config.Source) []string {
	secrets := make([]string, 0)
	for _, value := range source.Values {
		if value.Secret && value.Value != "" {
			secrets = append(secrets, value.Value)
		}
	}
	return secrets
}

func uniqueSecrets(secrets []string) []string {
	seen := make(map[string]bool, len(secrets))
	unique := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret == "" || seen[secret] {
			continue
		}
		seen[secret] = true
		unique = append(unique, secret)
	}
	return unique
}
