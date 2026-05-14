package redact

import (
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/r13v/llmgate/internal/core"
)

const shortSecretMaxLength = 8

var (
	bearerPattern           = regexp.MustCompile(`(?i)\b(Bearer\s+)([^\s"',;]+)`)
	litellmAPIKeyPattern    = regexp.MustCompile(`(?im)(["']?\bx-litellm-api-key\b["']?\s*:\s*)("[^"\r\n]*"|'[^'\r\n]*'|[^\s"',;]+)`)
	authTokenAssignPattern  = regexp.MustCompile(`(?im)\b(` + core.VarAnthropicAuthToken + `"?\s*(?:=|:)\s*)("[^"\r\n]*"|'[^'\r\n]*'|[^\s#,\r\n]+)`)
	credentialQueryPattern  = regexp.MustCompile(`(?i)([?&;](?:api[_-]?key|access[_-]?token|refresh[_-]?token|token)=)([^&#\s"',;]+)`)
	credentialAssignPattern = regexp.MustCompile(
		`(?im)\b((?:api[_-]?key|access[_-]?token|refresh[_-]?token|token|x-api-key)"?\s*(?:=|:)\s*)("[^"\r\n]*"|'[^'\r\n]*'|[^\s#,&\r\n]+)`,
	)
	skTokenPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9][A-Za-z0-9_-]*\b`)
)

type Options struct {
	KnownSecrets []string
	HomeDir      string
	GOOS         string
}

func Text(input string, opts Options) string {
	output := input
	output = replaceValueGroup(output, bearerPattern, 2)
	output = replaceValueGroup(output, litellmAPIKeyPattern, 2)
	output = replaceValueGroup(output, authTokenAssignPattern, 2)
	output = replaceValueGroupPreservingMasked(output, credentialQueryPattern, 2)
	output = replaceValueGroupPreservingMasked(output, credentialAssignPattern, 2)
	output = redactKnownSecrets(output, opts.KnownSecrets)
	output = skTokenPattern.ReplaceAllStringFunc(output, MaskSecret)
	if opts.HomeDir != "" {
		output = ShortenHomePaths(output, opts.HomeDir, opts.GOOS)
	}
	return output
}

func MaskSecret(secret string) string {
	if secret == "" {
		return "<empty>"
	}
	if strings.HasPrefix(secret, "sk-") {
		if len(secret) <= shortSecretMaxLength+len("sk-") {
			return "sk-[redacted]"
		}
		return "sk-..." + lastRunes(secret, 4)
	}
	if len(secret) <= shortSecretMaxLength {
		return "***"
	}
	return "***" + lastRunes(secret, 4)
}

func ShortenHomePath(path, home, targetOS string) string {
	if path == "" || home == "" {
		return path
	}

	windows := effectiveGOOS(targetOS) == "windows"
	home = trimTrailingSeparators(home)
	normalizedPath := normalizePathForCompare(path, windows)
	normalizedHome := normalizePathForCompare(home, windows)
	if windows {
		normalizedPath = strings.ToLower(normalizedPath)
		normalizedHome = strings.ToLower(normalizedHome)
	}

	if normalizedPath == normalizedHome {
		return "~"
	}

	separator := "/"
	if windows {
		separator = `\`
	}
	if strings.HasPrefix(normalizedPath, normalizedHome+separator) {
		return "~" + path[len(home):]
	}
	return path
}

func ShortenHomePaths(input, home, targetOS string) string {
	if input == "" || home == "" {
		return input
	}

	windows := effectiveGOOS(targetOS) == "windows"
	variants := homeVariants(home, windows)
	output := input
	for _, variant := range variants {
		output = replaceHomeVariant(output, variant, windows)
	}
	return output
}

func replaceValueGroup(input string, pattern *regexp.Regexp, valueGroup int) string {
	matches := pattern.FindAllStringSubmatchIndex(input, -1)
	if len(matches) == 0 {
		return input
	}

	var output strings.Builder
	last := 0
	for _, match := range matches {
		valueStart := match[valueGroup*2]
		valueEnd := match[valueGroup*2+1]
		if valueStart < 0 || valueEnd < 0 {
			continue
		}
		output.WriteString(input[last:valueStart])
		output.WriteString(maskMaybeQuoted(input[valueStart:valueEnd]))
		last = valueEnd
	}
	output.WriteString(input[last:])
	return output.String()
}

func replaceValueGroupPreservingMasked(input string, pattern *regexp.Regexp, valueGroup int) string {
	matches := pattern.FindAllStringSubmatchIndex(input, -1)
	if len(matches) == 0 {
		return input
	}

	var output strings.Builder
	last := 0
	for _, match := range matches {
		valueStart := match[valueGroup*2]
		valueEnd := match[valueGroup*2+1]
		if valueStart < 0 || valueEnd < 0 {
			continue
		}
		output.WriteString(input[last:valueStart])
		output.WriteString(maskMaybeQuotedPreservingMasked(input[valueStart:valueEnd]))
		last = valueEnd
	}
	output.WriteString(input[last:])
	return output.String()
}

func redactKnownSecrets(input string, secrets []string) string {
	known := make([]string, 0, len(secrets))
	seen := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if _, ok := seen[secret]; ok {
			continue
		}
		seen[secret] = struct{}{}
		known = append(known, secret)
	}
	sort.Slice(known, func(i, j int) bool {
		return len(known[i]) > len(known[j])
	})

	output := input
	for _, secret := range known {
		output = strings.ReplaceAll(output, secret, MaskSecret(secret))
	}
	return output
}

func maskMaybeQuoted(value string) string {
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' || first == '\'') && first == last {
			return string(first) + MaskSecret(value[1:len(value)-1]) + string(last)
		}
	}
	return MaskSecret(value)
}

func maskMaybeQuotedPreservingMasked(value string) string {
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' || first == '\'') && first == last {
			unquoted := value[1 : len(value)-1]
			if isMaskedValue(unquoted) {
				return value
			}
			return string(first) + MaskSecret(unquoted) + string(last)
		}
	}
	if isMaskedValue(value) {
		return value
	}
	return MaskSecret(value)
}

func isMaskedValue(value string) bool {
	return value == "***" ||
		strings.HasPrefix(value, "***") ||
		value == "sk-[redacted]" ||
		strings.HasPrefix(value, "sk-...")
}

func lastRunes(value string, n int) string {
	runes := []rune(value)
	if len(runes) <= n {
		return value
	}
	return string(runes[len(runes)-n:])
}

func effectiveGOOS(targetOS string) string {
	if targetOS == "" {
		return runtime.GOOS
	}
	return targetOS
}

func trimTrailingSeparators(path string) string {
	for len(path) > 1 && (strings.HasSuffix(path, "/") || strings.HasSuffix(path, `\`)) {
		path = path[:len(path)-1]
	}
	return path
}

func normalizePathForCompare(path string, windows bool) string {
	if windows {
		return strings.ReplaceAll(path, "/", `\`)
	}
	return path
}

func homeVariants(home string, windows bool) []string {
	trimmed := trimTrailingSeparators(home)
	variants := []string{trimmed}
	if windows {
		withBackslash := strings.ReplaceAll(trimmed, "/", `\`)
		withSlash := strings.ReplaceAll(trimmed, `\`, "/")
		variants = append(variants, withBackslash, withSlash)
	}

	seen := make(map[string]struct{}, len(variants))
	unique := variants[:0]
	for _, variant := range variants {
		if variant == "" {
			continue
		}
		key := variant
		if windows {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, variant)
	}
	sort.Slice(unique, func(i, j int) bool {
		return len(unique[i]) > len(unique[j])
	})
	return unique
}

func replaceHomeVariant(input, home string, windows bool) string {
	if home == "" {
		return input
	}

	compareInput := input
	compareHome := home
	if windows {
		compareInput = strings.ToLower(input)
		compareHome = strings.ToLower(home)
	}

	var output strings.Builder
	for index := 0; index < len(input); {
		if strings.HasPrefix(compareInput[index:], compareHome) && validHomeBoundary(input, index+len(home), windows) {
			output.WriteString("~")
			index += len(home)
			continue
		}
		output.WriteByte(input[index])
		index++
	}
	return output.String()
}

func validHomeBoundary(input string, index int, windows bool) bool {
	if index >= len(input) {
		return true
	}
	if input[index] == '/' {
		return true
	}
	return windows && input[index] == '\\'
}
