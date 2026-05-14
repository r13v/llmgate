package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/r13v/llmgate/internal/gateway"
)

type fakeGatewayOptions struct {
	models           []string
	acceptedTokens   []string
	listResponses    []gatewayResponse
	fallbackOnly     bool
	probeFailures    map[string]gatewayResponse
	failProbesAfter  int
	includeTokenBody bool
}

type gatewayResponse struct {
	status int
	body   string
	models []string
}

type fakeGateway struct {
	t      *testing.T
	server *httptest.Server

	mu              sync.Mutex
	models          []string
	acceptedTokens  map[string]bool
	listResponses   []gatewayResponse
	fallbackOnly    bool
	probeFailures   map[string]gatewayResponse
	failProbesAfter int
	tokenBody       bool

	listCalls       int
	fallbackCalls   int
	probeCalls      int
	paths           []string
	probedModels    []string
	probePingBodies int
}

func newFakeGateway(t *testing.T, opts fakeGatewayOptions) *fakeGateway {
	t.Helper()
	if len(opts.models) == 0 {
		opts.models = recommendedModels
	}
	accepted := map[string]bool{testToken: true, altTestToken: true}
	for _, token := range opts.acceptedTokens {
		accepted[token] = true
	}
	g := &fakeGateway{
		t:               t,
		models:          append([]string(nil), opts.models...),
		acceptedTokens:  accepted,
		listResponses:   append([]gatewayResponse(nil), opts.listResponses...),
		fallbackOnly:    opts.fallbackOnly,
		probeFailures:   opts.probeFailures,
		failProbesAfter: opts.failProbesAfter,
		tokenBody:       opts.includeTokenBody,
	}
	g.server = httptest.NewServer(http.HandlerFunc(g.serveHTTP))
	return g
}

func (g *fakeGateway) client() gateway.Client {
	return gateway.Client{
		HTTPClient: g.server.Client(),
		Timeout:    500 * time.Millisecond,
		Cache:      gateway.NewCache(),
	}
}

func (g *fakeGateway) url() string {
	return g.server.URL
}

func (g *fakeGateway) close() {
	if g != nil && g.server != nil {
		g.server.Close()
	}
}

func (g *fakeGateway) queueListResponses(responses ...gatewayResponse) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.listResponses = append(g.listResponses, responses...)
}

func (g *fakeGateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.paths = append(g.paths, r.URL.Path)
	g.mu.Unlock()

	switch r.URL.Path {
	case "/v1/models":
		if g.fallbackOnly {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"not found"}`))
			return
		}
		g.serveModels(w, r, false)
	case "/models":
		g.serveModels(w, r, true)
	case "/v1/chat/completions":
		g.serveProbe(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"not found"}`))
	}
}

func (g *fakeGateway) serveModels(w http.ResponseWriter, r *http.Request, fallback bool) {
	token := bearerToken(r)
	g.mu.Lock()
	if fallback {
		g.fallbackCalls++
	} else {
		g.listCalls++
	}
	response := gatewayResponse{status: http.StatusOK, models: append([]string(nil), g.models...)}
	if len(g.listResponses) > 0 {
		response = g.listResponses[0]
		g.listResponses = g.listResponses[1:]
	}
	g.mu.Unlock()

	if !g.acceptsToken(token) {
		w.WriteHeader(http.StatusUnauthorized)
		body := `{"detail":"gateway rejected token"}`
		if g.tokenBody {
			body = `{"detail":"gateway rejected token ` + token + `"}`
		}
		_, _ = w.Write([]byte(body))
		return
	}
	writeGatewayResponse(g.t, w, response)
}

func (g *fakeGateway) serveProbe(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r)
	var payload struct {
		Model    string `json:"model"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens int `json:"max_tokens"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		g.t.Fatalf("decode probe payload: %v", err)
	}

	g.mu.Lock()
	g.probeCalls++
	g.probedModels = append(g.probedModels, payload.Model)
	if len(payload.Messages) > 0 && payload.Messages[0].Content == "ping" && payload.MaxTokens == 1 {
		g.probePingBodies++
	}
	call := g.probeCalls
	g.mu.Unlock()

	if !g.acceptsToken(token) {
		w.WriteHeader(http.StatusUnauthorized)
		body := `{"detail":"probe rejected token"}`
		if g.tokenBody {
			body = `{"detail":"probe rejected token ` + token + `"}`
		}
		_, _ = w.Write([]byte(body))
		return
	}

	if g.failProbesAfter > 0 && call > g.failProbesAfter {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"detail":"probe failed after setup"}`))
		return
	}

	if response, ok := g.probeFailures[payload.Model]; ok {
		writeGatewayResponse(g.t, w, response)
		return
	}

	if !containsString(g.models, payload.Model) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"model unavailable"}`))
		return
	}
	writeJSON(g.t, w, map[string]any{
		"choices": []map[string]any{{"message": map[string]string{"content": ""}}},
	})
}

func (g *fakeGateway) acceptsToken(token string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.acceptedTokens[token]
}

func writeGatewayResponse(t *testing.T, w http.ResponseWriter, response gatewayResponse) {
	t.Helper()
	status := response.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if response.body != "" {
		_, _ = w.Write([]byte(response.body))
		return
	}
	models := response.models
	if models == nil {
		models = recommendedModels
	}
	data := make([]map[string]string, 0, len(models))
	for _, model := range models {
		data = append(data, map[string]string{"id": model})
	}
	writeJSON(t, w, map[string]any{"data": data})
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode JSON response: %v", err)
	}
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	return strings.TrimPrefix(header, "Bearer ")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
