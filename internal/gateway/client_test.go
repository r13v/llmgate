package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeModelURLs(t *testing.T) {
	tests := []struct {
		name         string
		baseURL      string
		wantBase     string
		wantPrimary  string
		wantFallback string
	}{
		{
			name:         "root",
			baseURL:      "https://gateway.example.com",
			wantBase:     "https://gateway.example.com",
			wantPrimary:  "https://gateway.example.com/v1/models",
			wantFallback: "https://gateway.example.com/models",
		},
		{
			name:         "v1 suffix",
			baseURL:      "https://gateway.example.com/v1",
			wantBase:     "https://gateway.example.com/v1",
			wantPrimary:  "https://gateway.example.com/v1/models",
			wantFallback: "https://gateway.example.com/models",
		},
		{
			name:         "path prefix",
			baseURL:      "https://gateway.example.com/litellm",
			wantBase:     "https://gateway.example.com/litellm",
			wantPrimary:  "https://gateway.example.com/litellm/v1/models",
			wantFallback: "https://gateway.example.com/litellm/models",
		},
		{
			name:         "path prefix with v1 and query hash trailing slash",
			baseURL:      "https://gateway.example.com/litellm/v1/?token=leak#fragment",
			wantBase:     "https://gateway.example.com/litellm/v1",
			wantPrimary:  "https://gateway.example.com/litellm/v1/models",
			wantFallback: "https://gateway.example.com/litellm/models",
		},
		{
			name:         "v1 models endpoint",
			baseURL:      "https://sk-secret123456@gateway.example.com/litellm/v1/models?token=leak#fragment",
			wantBase:     "https://gateway.example.com/litellm/v1",
			wantPrimary:  "https://gateway.example.com/litellm/v1/models",
			wantFallback: "https://gateway.example.com/litellm/models",
		},
		{
			name:         "models endpoint fallback form",
			baseURL:      "https://gateway.example.com/litellm/models",
			wantBase:     "https://gateway.example.com/litellm",
			wantPrimary:  "https://gateway.example.com/litellm/v1/models",
			wantFallback: "https://gateway.example.com/litellm/models",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeModelURLs(tt.baseURL)
			if err != nil {
				t.Fatalf("NormalizeModelURLs() error = %v", err)
			}
			if got.Base != tt.wantBase {
				t.Fatalf("Base = %q, want %q", got.Base, tt.wantBase)
			}
			if got.Primary != tt.wantPrimary {
				t.Fatalf("Primary = %q, want %q", got.Primary, tt.wantPrimary)
			}
			if got.Fallback != tt.wantFallback {
				t.Fatalf("Fallback = %q, want %q", got.Fallback, tt.wantFallback)
			}
		})
	}
}

func TestNormalizeCompletionsURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "root",
			baseURL: "https://gateway.example.com",
			want:    "https://gateway.example.com/v1/chat/completions",
		},
		{
			name:    "v1 suffix",
			baseURL: "https://gateway.example.com/v1",
			want:    "https://gateway.example.com/v1/chat/completions",
		},
		{
			name:    "prefix",
			baseURL: "https://gateway.example.com/proxy/?q=1#frag",
			want:    "https://gateway.example.com/proxy/v1/chat/completions",
		},
		{
			name:    "v1 models endpoint",
			baseURL: "https://gateway.example.com/proxy/v1/models",
			want:    "https://gateway.example.com/proxy/v1/chat/completions",
		},
		{
			name:    "chat completions endpoint",
			baseURL: "https://gateway.example.com/proxy/v1/chat/completions",
			want:    "https://gateway.example.com/proxy/v1/chat/completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeCompletionsURL(tt.baseURL)
			if err != nil {
				t.Fatalf("NormalizeCompletionsURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeCompletionsURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestListModelsSendsHeadersAndReturnsSortedUniqueIDs(t *testing.T) {
	const token = "sk-test-token-1234567890"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("Accept = %q, want application/json", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		if got := r.Header.Get("x-litellm-api-key"); got != token {
			t.Fatalf("x-litellm-api-key = %q, want token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"id":"claude-sonnet"},{"id":"claude-haiku"},{"id":"claude-sonnet"},{"id":""},{"id":42}]}`)
	}))
	defer server.Close()

	result, err := Client{}.ListModels(context.Background(), server.URL, token, RequestOptions{})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	want := []string{"claude-haiku", "claude-sonnet"}
	if !reflect.DeepEqual(result.Models, want) {
		t.Fatalf("Models = %v, want %v", result.Models, want)
	}
	if result.Cached {
		t.Fatalf("Cached = true, want false")
	}
}

func TestListModelsFallsBackOnlyOnPrimary404(t *testing.T) {
	var primaryHits atomic.Int32
	var fallbackHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			primaryHits.Add(1)
			http.NotFound(w, r)
		case "/models":
			fallbackHits.Add(1)
			_, _ = fmt.Fprint(w, `{"data":[{"id":"claude-sonnet"}]}`)
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	result, err := Client{}.ListModels(context.Background(), server.URL, "token", RequestOptions{})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if !result.FallbackUsed {
		t.Fatalf("FallbackUsed = false, want true")
	}
	if !strings.Contains(result.Summary, "/models fallback") {
		t.Fatalf("Summary = %q, want fallback mention", result.Summary)
	}
	if primaryHits.Load() != 1 || fallbackHits.Load() != 1 {
		t.Fatalf("hits primary=%d fallback=%d, want 1 each", primaryHits.Load(), fallbackHits.Load())
	}
}

func TestListModelsDoesNotFallbackForNon404(t *testing.T) {
	var fallbackHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			fallbackHits.Add(1)
			_, _ = fmt.Fprint(w, `{"data":[{"id":"claude-sonnet"}]}`)
			return
		}
		http.Error(w, `{"message":"upstream failed"}`, http.StatusBadGateway)
	}))
	defer server.Close()

	_, err := Client{}.ListModels(context.Background(), server.URL, "token", RequestOptions{})
	_ = assertFailure(t, err, FailureHTTP)
	if fallbackHits.Load() != 0 {
		t.Fatalf("fallback hits = %d, want 0", fallbackHits.Load())
	}
}

func TestListModelsFailureClassification(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		want       FailureKind
		wantDetail string
	}{
		{
			name:       "auth",
			status:     http.StatusUnauthorized,
			body:       `{"detail":"bad token"}`,
			want:       FailureAuth,
			wantDetail: "gateway rejected the token",
		},
		{
			name:       "http",
			status:     http.StatusInternalServerError,
			body:       `{"error":{"message":"upstream sk-test-token-1234567890 failed"}}`,
			want:       FailureHTTP,
			wantDetail: "upstream sk-...7890 failed",
		},
		{
			name:   "invalid json",
			status: http.StatusOK,
			body:   `{"data":`,
			want:   FailureInvalidJSON,
		},
		{
			name:   "empty models",
			status: http.StatusOK,
			body:   `{"data":[{"id":""},{"id":42}]}`,
			want:   FailureEmptyModels,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			_, err := Client{}.ListModels(context.Background(), server.URL, "sk-test-token-1234567890", RequestOptions{})
			gatewayErr := assertFailure(t, err, tt.want)
			if tt.wantDetail != "" && !strings.Contains(gatewayErr.Detail, tt.wantDetail) {
				t.Fatalf("Detail = %q, want to contain %q", gatewayErr.Detail, tt.wantDetail)
			}
			if strings.Contains(gatewayErr.Error(), "sk-test-token-1234567890") {
				t.Fatalf("error leaked token: %v", gatewayErr)
			}
		})
	}
}

func TestListModelsInvalidURLAndNetworkFailure(t *testing.T) {
	_, err := Client{}.ListModels(context.Background(), "gateway.example.com", "token", RequestOptions{})
	_ = assertFailure(t, err, FailureInvalidURL)

	client := Client{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp sk-test-token-1234567890 failed")
		})},
	}
	_, err = client.ListModels(context.Background(), "https://gateway.example.com", "sk-test-token-1234567890", RequestOptions{})
	gatewayErr := assertFailure(t, err, FailureNetwork)
	if strings.Contains(gatewayErr.Detail, "sk-test-token-1234567890") {
		t.Fatalf("network detail leaked token: %q", gatewayErr.Detail)
	}
}

func TestSanitizedDetailsAreTruncated(t *testing.T) {
	const token = "sk-test-token-1234567890"
	longDetail := strings.Repeat("detail "+token+" ", 80)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": longDetail})
	}))
	defer server.Close()

	_, err := Client{}.ListModels(context.Background(), server.URL, token, RequestOptions{})
	gatewayErr := assertFailure(t, err, FailureHTTP)
	if got := len([]rune(gatewayErr.Detail)); got > maxDetailLength {
		t.Fatalf("detail length = %d, want <= %d", got, maxDetailLength)
	}
	if strings.Contains(gatewayErr.Detail, token) {
		t.Fatalf("detail leaked token: %q", gatewayErr.Detail)
	}
}

func TestExplainFailureUsesStructuredGatewayError(t *testing.T) {
	err := &Error{
		Kind:       FailureAuth,
		Operation:  "model list",
		StatusCode: http.StatusUnauthorized,
		URL:        "https://gateway.example.com/v1/models",
		Detail:     "Key Hash abc123 LiteLLM_VerificationTokenTable gateway rejected token " + strings.Repeat("x", 240),
		Cached:     true,
	}

	explanation := ExplainFailure(err)

	if explanation.Cause != "The gateway rejected the configured ANTHROPIC_AUTH_TOKEN." {
		t.Fatalf("Cause = %q", explanation.Cause)
	}
	for _, want := range []string{
		"operation: model list",
		"request URL: https://gateway.example.com/v1/models",
		"failure kind: auth",
		"HTTP status: 401",
		"cached failure: true",
	} {
		if !containsString(explanation.Evidence, want) {
			t.Fatalf("Evidence missing %q: %#v", want, explanation.Evidence)
		}
	}
	message := evidenceWithPrefix(explanation.Evidence, "gateway message: ")
	if message == "" {
		t.Fatalf("Evidence missing gateway message: %#v", explanation.Evidence)
	}
	if strings.Contains(message, "LiteLLM_VerificationTokenTable") {
		t.Fatalf("gateway message kept noisy raw detail: %q", message)
	}
	if !strings.Contains(message, "review details for the full response") {
		t.Fatalf("gateway message = %q, want concise noisy-detail summary", message)
	}
	if explanation.Remediation != "Update ANTHROPIC_AUTH_TOKEN for the active source, or remove the stale override." {
		t.Fatalf("Remediation = %q", explanation.Remediation)
	}
}

func TestExplainFailureCoversFailureKinds(t *testing.T) {
	tests := []struct {
		name            string
		kind            FailureKind
		wantCause       string
		wantRemediation string
	}{
		{
			name:            "auth",
			kind:            FailureAuth,
			wantCause:       "The gateway rejected the configured ANTHROPIC_AUTH_TOKEN.",
			wantRemediation: "Update ANTHROPIC_AUTH_TOKEN for the active source, or remove the stale override.",
		},
		{
			name:            "network",
			kind:            FailureNetwork,
			wantCause:       "llmgate could not reach the configured gateway.",
			wantRemediation: "Check ANTHROPIC_BASE_URL, DNS, VPN/proxy, TLS, and network access.",
		},
		{
			name:            "invalid url",
			kind:            FailureInvalidURL,
			wantCause:       "ANTHROPIC_BASE_URL is not a valid gateway URL.",
			wantRemediation: "Set ANTHROPIC_BASE_URL to an http(s) LiteLLM gateway root or /v1 URL.",
		},
		{
			name:            "invalid json",
			kind:            FailureInvalidJSON,
			wantCause:       "The gateway response was not OpenAI-compatible JSON.",
			wantRemediation: "Inspect the gateway response and OpenAI-compatible model-list route.",
		},
		{
			name:            "empty models",
			kind:            FailureEmptyModels,
			wantCause:       "The gateway returned no usable model IDs.",
			wantRemediation: "Configure the gateway to expose at least one usable model ID.",
		},
		{
			name:            "http",
			kind:            FailureHTTP,
			wantCause:       "The gateway returned a non-success HTTP response.",
			wantRemediation: "Inspect the gateway/upstream logs, base URL, and selected model routing.",
		},
		{
			name:            "unknown",
			kind:            FailureKind("custom"),
			wantCause:       defaultFailureCause(),
			wantRemediation: defaultFailureRemediation(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			explanation := ExplainFailure(&Error{
				Kind:       tt.kind,
				Operation:  "model list",
				StatusCode: http.StatusBadGateway,
				URL:        "https://gateway.example.com/v1/models",
				Detail:     "plain gateway detail",
			})

			if explanation.Cause != tt.wantCause {
				t.Fatalf("Cause = %q, want %q", explanation.Cause, tt.wantCause)
			}
			if explanation.Remediation != tt.wantRemediation {
				t.Fatalf("Remediation = %q, want %q", explanation.Remediation, tt.wantRemediation)
			}
			if tt.kind != "" && !containsString(explanation.Evidence, "failure kind: "+string(tt.kind)) {
				t.Fatalf("Evidence missing failure kind %q: %#v", tt.kind, explanation.Evidence)
			}
			if !containsString(explanation.Evidence, "gateway message: plain gateway detail") {
				t.Fatalf("Evidence missing gateway detail: %#v", explanation.Evidence)
			}
		})
	}
}

func TestExplainFailureHandlesPlainError(t *testing.T) {
	explanation := ExplainFailure(errors.New("dial tcp failed"))

	if explanation.Cause == "" || explanation.Remediation == "" {
		t.Fatalf("explanation missing cause/remediation: %#v", explanation)
	}
	if !containsString(explanation.Evidence, "reason: dial tcp failed") {
		t.Fatalf("Evidence = %#v, want plain reason", explanation.Evidence)
	}
}

func TestExplainFailureHandlesNil(t *testing.T) {
	explanation := ExplainFailure(nil)
	if explanation.Cause != "" || len(explanation.Evidence) != 0 || explanation.Remediation != "" {
		t.Fatalf("ExplainFailure(nil) = %#v, want empty explanation", explanation)
	}
}

func TestResponseBodySizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(w, strings.Repeat("x", maxResponseBodyBytes+1))
	}))
	defer server.Close()

	_, err := Client{}.ListModels(context.Background(), server.URL, "token", RequestOptions{})
	gatewayErr := assertFailure(t, err, FailureHTTP)
	if !strings.Contains(gatewayErr.Detail, "exceeded") {
		t.Fatalf("Detail = %q, want body limit error", gatewayErr.Detail)
	}
}

func TestProbeModelSendsPingChatCompletion(t *testing.T) {
	const token = "sk-test-token-1234567890"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", got)
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.Model != "claude-sonnet" || body.MaxTokens != 1 {
			t.Fatalf("body model/max_tokens = %q/%d, want claude-sonnet/1", body.Model, body.MaxTokens)
		}
		if len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "ping" {
			t.Fatalf("messages = %+v, want user ping", body.Messages)
		}
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	result, err := Client{}.ProbeModel(context.Background(), server.URL, token, "claude-sonnet", RequestOptions{})
	if err != nil {
		t.Fatalf("ProbeModel() error = %v", err)
	}
	if result.Model != "claude-sonnet" {
		t.Fatalf("Model = %q, want claude-sonnet", result.Model)
	}
}

func TestProbeModelFailureClassification(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   FailureKind
	}{
		{name: "auth", status: http.StatusForbidden, body: `{"message":"bad token"}`, want: FailureAuth},
		{name: "http", status: http.StatusBadGateway, body: `{"message":"model unavailable"}`, want: FailureHTTP},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			_, err := Client{}.ProbeModel(context.Background(), server.URL, "token", "claude-sonnet", RequestOptions{})
			_ = assertFailure(t, err, tt.want)
		})
	}

	_, err := Client{}.ProbeModel(context.Background(), "://bad-url", "token", "claude-sonnet", RequestOptions{})
	_ = assertFailure(t, err, FailureInvalidURL)
}

func TestClientCacheReusesSuccessesAndFailuresWithBypass(t *testing.T) {
	var hits atomic.Int32
	fail := atomic.Bool{}
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		if fail.Load() {
			http.Error(w, "upstream failed", http.StatusBadGateway)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":[{"id":"claude-sonnet"}]}`)
	}))
	defer server.Close()

	client := Client{Cache: NewCache()}
	_, err := client.ListModels(context.Background(), server.URL, "token-a", RequestOptions{})
	_ = assertFailure(t, err, FailureHTTP)
	if hits.Load() != 1 {
		t.Fatalf("hits after first failure = %d, want 1", hits.Load())
	}

	fail.Store(false)
	_, err = client.ListModels(context.Background(), server.URL, "token-a", RequestOptions{})
	cachedErr := assertFailure(t, err, FailureHTTP)
	if !cachedErr.Cached {
		t.Fatalf("cached failure Cached = false, want true")
	}
	if hits.Load() != 1 {
		t.Fatalf("hits after cached failure = %d, want 1", hits.Load())
	}

	result, err := client.ListModels(context.Background(), server.URL, "token-a", RequestOptions{BypassFailedCache: true})
	if err != nil {
		t.Fatalf("ListModels() bypass error = %v", err)
	}
	if result.Cached {
		t.Fatalf("bypass result Cached = true, want false")
	}
	if hits.Load() != 2 {
		t.Fatalf("hits after bypass = %d, want 2", hits.Load())
	}

	result, err = client.ListModels(context.Background(), server.URL+"?ignored=1#fragment", "token-a", RequestOptions{})
	if err != nil {
		t.Fatalf("ListModels() cached success error = %v", err)
	}
	if !result.Cached || !strings.Contains(result.Summary, "(cached)") {
		t.Fatalf("cached success = %+v, want cached summary", result)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits after cached success = %d, want 2", hits.Load())
	}

	_, err = client.ListModels(context.Background(), server.URL, "token-b", RequestOptions{})
	if err != nil {
		t.Fatalf("ListModels() token-b error = %v", err)
	}
	if hits.Load() != 3 {
		t.Fatalf("hits with different token = %d, want 3", hits.Load())
	}
	if strings.Contains(client.Cache.String(), "token") {
		t.Fatalf("cache string leaked token: %s", client.Cache)
	}
}

func TestProbeCacheKeysByModelAndBypassesFailedCache(t *testing.T) {
	var hits atomic.Int32
	fail := atomic.Bool{}
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if fail.Load() {
			http.Error(w, "model failed", http.StatusBadGateway)
			return
		}
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	client := Client{Cache: NewCache()}
	_, err := client.ProbeModel(context.Background(), server.URL, "token", "claude-sonnet", RequestOptions{})
	_ = assertFailure(t, err, FailureHTTP)

	fail.Store(false)
	_, err = client.ProbeModel(context.Background(), server.URL, "token", "claude-sonnet", RequestOptions{})
	cachedErr := assertFailure(t, err, FailureHTTP)
	if !cachedErr.Cached {
		t.Fatalf("probe cached failure Cached = false, want true")
	}
	if hits.Load() != 1 {
		t.Fatalf("hits after cached probe failure = %d, want 1", hits.Load())
	}

	result, err := client.ProbeModel(context.Background(), server.URL, "token", "claude-sonnet", RequestOptions{BypassFailedCache: true})
	if err != nil {
		t.Fatalf("ProbeModel() bypass error = %v", err)
	}
	if result.Cached {
		t.Fatalf("bypass probe Cached = true, want false")
	}
	if hits.Load() != 2 {
		t.Fatalf("hits after probe bypass = %d, want 2", hits.Load())
	}

	result, err = client.ProbeModel(context.Background(), server.URL, "token", "claude-sonnet", RequestOptions{})
	if err != nil {
		t.Fatalf("ProbeModel() cached success error = %v", err)
	}
	if !result.Cached || !strings.Contains(result.Summary, "(cached)") {
		t.Fatalf("cached probe success = %+v, want cached summary", result)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits after cached probe success = %d, want 2", hits.Load())
	}

	_, err = client.ProbeModel(context.Background(), server.URL, "token", "claude-haiku", RequestOptions{})
	if err != nil {
		t.Fatalf("ProbeModel() different model error = %v", err)
	}
	if hits.Load() != 3 {
		t.Fatalf("hits after different model = %d, want 3", hits.Load())
	}
}

func TestTimeoutClassifiesAsNetworkFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = fmt.Fprint(w, `{"data":[{"id":"claude-sonnet"}]}`)
	}))
	defer server.Close()

	client := Client{Timeout: time.Millisecond}
	_, err := client.ListModels(context.Background(), server.URL, "token", RequestOptions{})
	_ = assertFailure(t, err, FailureNetwork)
}

func assertFailure(t *testing.T, err error, want FailureKind) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", want)
	}
	var gatewayErr *Error
	if !errors.As(err, &gatewayErr) {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if gatewayErr.Kind != want {
		t.Fatalf("error kind = %s, want %s: %v", gatewayErr.Kind, want, gatewayErr)
	}
	return gatewayErr
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func evidenceWithPrefix(values []string, prefix string) string {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return value
		}
	}
	return ""
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
