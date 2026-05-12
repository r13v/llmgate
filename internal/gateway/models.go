package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/r13v/llmgate/internal/redact"
)

const maxDetailLength = 500

var whitespacePattern = regexp.MustCompile(`\s+`)

type ModelURLs struct {
	Primary  string
	Fallback string
}

func NormalizeModelURLs(baseURL string) (ModelURLs, error) {
	base, basePath, endsV1, err := normalizeBaseURL(baseURL)
	if err != nil {
		return ModelURLs{}, err
	}

	primary := cloneURL(base)
	if endsV1 {
		primary.Path = joinURLPath(basePath, "models")
	} else {
		primary.Path = joinURLPath(basePath, "v1", "models")
	}

	fallbackRoot := basePath
	if endsV1 {
		fallbackRoot = trimV1Suffix(basePath)
	}
	fallback := cloneURL(base)
	fallback.Path = joinURLPath(fallbackRoot, "models")

	return ModelURLs{
		Primary:  primary.String(),
		Fallback: fallback.String(),
	}, nil
}

func NormalizeCompletionsURL(baseURL string) (string, error) {
	base, basePath, endsV1, err := normalizeBaseURL(baseURL)
	if err != nil {
		return "", err
	}

	completions := cloneURL(base)
	if endsV1 {
		completions.Path = joinURLPath(basePath, "chat", "completions")
	} else {
		completions.Path = joinURLPath(basePath, "v1", "chat", "completions")
	}
	return completions.String(), nil
}

func normalizeBaseURL(raw string) (*url.URL, string, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, "", false, fmt.Errorf("gateway base URL is empty")
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return nil, "", false, fmt.Errorf("invalid gateway base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, "", false, fmt.Errorf("invalid gateway base URL: scheme must be http or https")
	}
	if parsed.Host == "" {
		return nil, "", false, fmt.Errorf("invalid gateway base URL: host is required")
	}

	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	parsed.RawPath = ""
	basePath := cleanURLPath(parsed.Path)
	basePath = stripKnownEndpointSuffix(basePath)
	parsed.Path = basePath

	return parsed, basePath, pathEndsWithV1(basePath), nil
}

func cloneURL(value *url.URL) *url.URL {
	cloned := *value
	return &cloned
}

func cleanURLPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return ""
	}
	value = path.Clean("/" + strings.Trim(value, "/"))
	if value == "/" {
		return ""
	}
	return value
}

func pathEndsWithV1(value string) bool {
	value = strings.Trim(value, "/")
	if value == "" {
		return false
	}
	parts := strings.Split(value, "/")
	return strings.EqualFold(parts[len(parts)-1], "v1")
}

func stripKnownEndpointSuffix(value string) string {
	switch {
	case pathHasSuffix(value, "v1", "models"):
		return trimPathSuffix(value, 1)
	case pathHasSuffix(value, "models"):
		return trimPathSuffix(value, 1)
	case pathHasSuffix(value, "v1", "chat", "completions"):
		return trimPathSuffix(value, 2)
	case pathHasSuffix(value, "chat", "completions"):
		return trimPathSuffix(value, 2)
	default:
		return value
	}
}

func pathHasSuffix(value string, suffix ...string) bool {
	parts := pathParts(value)
	if len(parts) < len(suffix) {
		return false
	}
	offset := len(parts) - len(suffix)
	for i, want := range suffix {
		if !strings.EqualFold(parts[offset+i], want) {
			return false
		}
	}
	return true
}

func trimPathSuffix(value string, count int) string {
	parts := pathParts(value)
	if count >= len(parts) {
		return ""
	}
	return "/" + strings.Join(parts[:len(parts)-count], "/")
}

func pathParts(value string) []string {
	value = strings.Trim(value, "/")
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}

func trimV1Suffix(value string) string {
	if !pathEndsWithV1(value) {
		return value
	}
	parts := strings.Split(strings.Trim(value, "/"), "/")
	parts = parts[:len(parts)-1]
	if len(parts) == 0 {
		return ""
	}
	return "/" + strings.Join(parts, "/")
}

func joinURLPath(base string, parts ...string) string {
	all := make([]string, 0, len(parts)+1)
	if base != "" {
		all = append(all, strings.Trim(base, "/"))
	}
	for _, part := range parts {
		if part != "" {
			all = append(all, strings.Trim(part, "/"))
		}
	}
	if len(all) == 0 {
		return ""
	}
	return "/" + strings.Join(all, "/")
}

func parseModelIDs(data []byte) ([]string, error) {
	var response struct {
		Data []any `json:"data"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(response.Data))
	models := make([]string, 0, len(response.Data))
	for _, item := range response.Data {
		model, ok := item.(map[string]any)
		if !ok {
			continue
		}
		rawID, ok := model["id"].(string)
		if !ok {
			continue
		}
		id := strings.TrimSpace(rawID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, id)
	}
	sort.Strings(models)
	return models, nil
}

func sanitizedResponseDetail(data []byte, token string) string {
	detail := usefulResponseDetail(data)
	detail = redact.Text(detail, redact.Options{KnownSecrets: []string{token}})
	detail = strings.TrimSpace(detail)
	return truncateRunes(detail, maxDetailLength)
}

func usefulResponseDetail(data []byte) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return ""
	}

	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err == nil {
		values := collectDetailValues(decoded)
		if len(values) > 0 {
			return strings.Join(values, "; ")
		}
		if compacted, err := compactJSON(trimmed); err == nil {
			return string(compacted)
		}
	}

	return normalizeWhitespace(string(trimmed))
}

func collectDetailValues(value any) []string {
	var values []string
	collectDetailValuesInto(value, "", &values)
	return values
}

func collectDetailValuesInto(value any, key string, values *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for _, name := range []string{"detail", "message", "error", "error_description"} {
			member, ok := typed[name]
			if !ok {
				continue
			}
			collectDetailValuesInto(member, name, values)
		}
	case []any:
		for _, member := range typed {
			collectDetailValuesInto(member, key, values)
		}
	case string:
		if isDetailKey(key) {
			if text := normalizeWhitespace(typed); text != "" {
				*values = append(*values, text)
			}
		}
	case float64, bool:
		if isDetailKey(key) {
			*values = append(*values, fmt.Sprint(typed))
		}
	}
}

func isDetailKey(key string) bool {
	switch key {
	case "detail", "message", "error", "error_description":
		return true
	default:
		return false
	}
}

func compactJSON(data []byte) ([]byte, error) {
	var buffer bytes.Buffer
	if err := json.Compact(&buffer, data); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func normalizeWhitespace(value string) string {
	return strings.TrimSpace(whitespacePattern.ReplaceAllString(value, " "))
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
