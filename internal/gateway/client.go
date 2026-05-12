package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultTimeout = 10 * time.Second
const maxResponseBodyBytes = 1 << 20

var errResponseBodyTooLarge = errors.New("gateway response body exceeded 1048576 bytes")

type FailureKind string

const (
	FailureAuth        FailureKind = "auth"
	FailureHTTP        FailureKind = "http"
	FailureInvalidJSON FailureKind = "invalid-json"
	FailureEmptyModels FailureKind = "empty-models"
	FailureNetwork     FailureKind = "network"
	FailureInvalidURL  FailureKind = "invalid-url"
)

type Client struct {
	HTTPClient *http.Client
	Timeout    time.Duration
	Cache      *Cache
}

type RequestOptions struct {
	BypassFailedCache bool
}

type ModelListResult struct {
	Models       []string
	URL          string
	FallbackURL  string
	FallbackUsed bool
	Cached       bool
	Summary      string
}

type ProbeResult struct {
	URL     string
	Model   string
	Cached  bool
	Summary string
}

type Error struct {
	Kind       FailureKind
	Operation  string
	StatusCode int
	URL        string
	Detail     string
	Cached     bool
	Err        error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}

	var builder strings.Builder
	if e.Operation != "" {
		builder.WriteString(e.Operation)
		builder.WriteString(" failed")
	} else {
		builder.WriteString("gateway request failed")
	}
	if e.Cached {
		builder.WriteString(" (cached)")
	}
	if e.Kind != "" {
		builder.WriteString(": ")
		builder.WriteString(string(e.Kind))
	}
	if e.StatusCode != 0 {
		_, _ = fmt.Fprintf(&builder, " HTTP %d", e.StatusCode)
	}
	if e.Detail != "" {
		builder.WriteString(": ")
		builder.WriteString(e.Detail)
	}
	if e.Err != nil && e.Detail == "" {
		builder.WriteString(": ")
		builder.WriteString(e.Err.Error())
	}
	return builder.String()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *Error) cacheCopy() *Error {
	if e == nil {
		return nil
	}
	copied := *e
	copied.Cached = false
	return &copied
}

func (e *Error) cachedCopy() *Error {
	if e == nil {
		return nil
	}
	copied := *e
	copied.Cached = true
	return &copied
}

func (c Client) ListModels(ctx context.Context, baseURL, token string, opts RequestOptions) (ModelListResult, error) {
	modelURLs, err := NormalizeModelURLs(baseURL)
	if err != nil {
		return ModelListResult{}, &Error{
			Kind:      FailureInvalidURL,
			Operation: "model list",
			Detail:    sanitizedResponseDetail([]byte(err.Error()), token),
			Err:       err,
		}
	}

	cacheKey := cacheKey(modelURLs.Primary, modelURLs.Fallback, token)
	if result, cachedErr, ok := c.Cache.getModelList(cacheKey, opts.BypassFailedCache); ok {
		if cachedErr != nil {
			return ModelListResult{}, cachedErr
		}
		return result, nil
	}

	result, requestErr := c.listModelsUncached(ctx, modelURLs, token)
	if requestErr != nil {
		c.Cache.setModelList(cacheKey, ModelListResult{}, requestErr)
		return ModelListResult{}, requestErr
	}
	c.Cache.setModelList(cacheKey, result, nil)
	return result, nil
}

func (c Client) ProbeModel(ctx context.Context, baseURL, token, model string, opts RequestOptions) (ProbeResult, error) {
	completionsURL, err := NormalizeCompletionsURL(baseURL)
	if err != nil {
		return ProbeResult{}, &Error{
			Kind:      FailureInvalidURL,
			Operation: "model probe",
			Detail:    sanitizedResponseDetail([]byte(err.Error()), token),
			Err:       err,
		}
	}

	cacheKey := cacheKey(completionsURL, token, model)
	if result, cachedErr, ok := c.Cache.getProbe(cacheKey, opts.BypassFailedCache); ok {
		if cachedErr != nil {
			return ProbeResult{}, cachedErr
		}
		return result, nil
	}

	result, probeErr := c.probeModelUncached(ctx, completionsURL, token, model)
	if probeErr != nil {
		c.Cache.setProbe(cacheKey, ProbeResult{}, probeErr)
		return ProbeResult{}, probeErr
	}
	c.Cache.setProbe(cacheKey, result, nil)
	return result, nil
}

func (c Client) listModelsUncached(ctx context.Context, modelURLs ModelURLs, token string) (ModelListResult, *Error) {
	status, body, err := c.doJSON(ctx, http.MethodGet, modelURLs.Primary, token, nil)
	if err != nil {
		return ModelListResult{}, requestError("model list", modelURLs.Primary, token, err)
	}
	if status == http.StatusNotFound {
		fallbackStatus, fallbackBody, fallbackErr := c.doJSON(ctx, http.MethodGet, modelURLs.Fallback, token, nil)
		if fallbackErr != nil {
			return ModelListResult{}, requestError("model list", modelURLs.Fallback, token, fallbackErr)
		}
		result, modelErr := handleModelListResponse(fallbackStatus, fallbackBody, token, modelURLs.Fallback, true)
		result.FallbackURL = modelURLs.Fallback
		return result, modelErr
	}
	result, modelErr := handleModelListResponse(status, body, token, modelURLs.Primary, false)
	result.FallbackURL = modelURLs.Fallback
	return result, modelErr
}

func (c Client) probeModelUncached(ctx context.Context, completionsURL, token, model string) (ProbeResult, *Error) {
	payload := map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ProbeResult{}, &Error{Kind: FailureHTTP, Operation: "model probe", URL: completionsURL, Err: err}
	}

	status, responseBody, err := c.doJSON(ctx, http.MethodPost, completionsURL, token, body)
	if err != nil {
		return ProbeResult{}, requestError("model probe", completionsURL, token, err)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return ProbeResult{}, &Error{
			Kind:       FailureAuth,
			Operation:  "model probe",
			StatusCode: status,
			URL:        completionsURL,
			Detail:     probeAuthDetail(responseBody, token),
		}
	}
	if status < 200 || status > 299 {
		return ProbeResult{}, &Error{
			Kind:       FailureHTTP,
			Operation:  "model probe",
			StatusCode: status,
			URL:        completionsURL,
			Detail:     sanitizedResponseDetail(responseBody, token),
		}
	}

	return ProbeResult{
		URL:     completionsURL,
		Model:   model,
		Summary: fmt.Sprintf("probe accepted model %q", model),
	}, nil
}

func (c Client) doJSON(ctx context.Context, method, requestURL, token string, body []byte) (int, []byte, error) {
	requestCtx, cancel := context.WithTimeout(ctx, c.effectiveTimeout())
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(requestCtx, method, requestURL, reader)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("x-litellm-api-key", token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient().Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() {
		_ = response.Body.Close()
	}()

	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodyBytes+1))
	if err != nil {
		return response.StatusCode, nil, err
	}
	if int64(len(data)) > maxResponseBodyBytes {
		return response.StatusCode, nil, errResponseBodyTooLarge
	}
	return response.StatusCode, data, nil
}

func (c Client) effectiveTimeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return defaultTimeout
}

func (c Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func handleModelListResponse(status int, body []byte, token, requestURL string, fallbackUsed bool) (ModelListResult, *Error) {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return ModelListResult{}, &Error{
			Kind:       FailureAuth,
			Operation:  "model list",
			StatusCode: status,
			URL:        requestURL,
			Detail:     modelAuthDetail(body, token),
		}
	}
	if status < 200 || status > 299 {
		return ModelListResult{}, &Error{
			Kind:       FailureHTTP,
			Operation:  "model list",
			StatusCode: status,
			URL:        requestURL,
			Detail:     sanitizedResponseDetail(body, token),
		}
	}

	models, err := parseModelIDs(body)
	if err != nil {
		return ModelListResult{}, &Error{
			Kind:      FailureInvalidJSON,
			Operation: "model list",
			URL:       requestURL,
			Detail:    sanitizedResponseDetail(body, token),
			Err:       err,
		}
	}
	if len(models) == 0 {
		return ModelListResult{}, &Error{
			Kind:      FailureEmptyModels,
			Operation: "model list",
			URL:       requestURL,
			Detail:    sanitizedResponseDetail(body, token),
		}
	}

	result := ModelListResult{
		Models:       models,
		URL:          requestURL,
		FallbackUsed: fallbackUsed,
		Summary:      fmt.Sprintf("listed %d model(s)", len(models)),
	}
	if fallbackUsed {
		result.Summary += " via /models fallback"
	}
	return result, nil
}

func requestError(operation, requestURL, token string, err error) *Error {
	if errors.Is(err, errResponseBodyTooLarge) {
		return &Error{
			Kind:      FailureHTTP,
			Operation: operation,
			URL:       requestURL,
			Detail:    sanitizedResponseDetail([]byte(err.Error()), token),
			Err:       err,
		}
	}
	return networkError(operation, requestURL, token, err)
}

func networkError(operation, requestURL, token string, err error) *Error {
	return &Error{
		Kind:      FailureNetwork,
		Operation: operation,
		URL:       requestURL,
		Detail:    sanitizedResponseDetail([]byte(err.Error()), token),
		Err:       err,
	}
}

func modelAuthDetail(body []byte, token string) string {
	detail := sanitizedResponseDetail(body, token)
	if detail == "" {
		return "gateway rejected the token"
	}
	return "gateway rejected the token: " + detail
}

func probeAuthDetail(body []byte, token string) string {
	detail := sanitizedResponseDetail(body, token)
	if detail == "" {
		return "gateway rejected the token during probe"
	}
	return "gateway rejected the token during probe: " + detail
}

func IsFailure(err error, kind FailureKind) bool {
	var gatewayErr *Error
	if !errors.As(err, &gatewayErr) {
		return false
	}
	return gatewayErr.Kind == kind
}
