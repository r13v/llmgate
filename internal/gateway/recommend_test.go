package gateway

import "testing"

func TestRecommendPrefersSonnetPrimaryAndFallsBackMissingTiersToPrimary(t *testing.T) {
	recommendation, ok := Recommend([]string{
		"gpt-4",
		"anthropic/claude-3-haiku-20240307",
		"anthropic/claude-4-sonnet-20250514",
	})
	if !ok {
		t.Fatalf("Recommend() ok = false, want true")
	}
	if recommendation.Primary != "anthropic/claude-4-sonnet-20250514" {
		t.Fatalf("Primary = %q, want sonnet", recommendation.Primary)
	}
	if recommendation.Haiku != "anthropic/claude-3-haiku-20240307" {
		t.Fatalf("Haiku = %q, want haiku", recommendation.Haiku)
	}
	if recommendation.Sonnet != recommendation.Primary {
		t.Fatalf("Sonnet = %q, want primary", recommendation.Sonnet)
	}
	if recommendation.Opus != recommendation.Primary {
		t.Fatalf("Opus = %q, want primary fallback", recommendation.Opus)
	}
}

func TestRecommendPrimaryFallsBackToOpusThenHaiku(t *testing.T) {
	recommendation, ok := Recommend([]string{
		"claude-3-opus-20240229",
		"claude-3-haiku-20240307",
	})
	if !ok {
		t.Fatalf("Recommend() opus ok = false, want true")
	}
	if recommendation.Primary != "claude-3-opus-20240229" {
		t.Fatalf("Primary = %q, want opus", recommendation.Primary)
	}

	recommendation, ok = Recommend([]string{"claude-3-haiku-20240307"})
	if !ok {
		t.Fatalf("Recommend() haiku ok = false, want true")
	}
	if recommendation.Primary != "claude-3-haiku-20240307" {
		t.Fatalf("Primary = %q, want haiku", recommendation.Primary)
	}
}

func TestRecommendReturnsFalseWithoutClaudeTierModels(t *testing.T) {
	if recommendation, ok := Recommend([]string{"gpt-4", "sonnet-without-vendor"}); ok {
		t.Fatalf("Recommend() = %+v, true; want false", recommendation)
	}
}

func TestBestModelOrdering(t *testing.T) {
	models := []string{
		"anthropic/claude.99-sonnet-stable",
		"anthropic/claude-3-sonnet-20240229",
		"anthropic/claude-4-sonnet-20240101-preview",
		"anthropic/claude-4-sonnet-20250101-beta",
		"anthropic/claude-4-sonnet-20250101",
		"anthropic/claude-4-sonnet-20250101-experimental",
	}

	recommendation, ok := Recommend(models)
	if !ok {
		t.Fatalf("Recommend() ok = false, want true")
	}
	if recommendation.Primary != "anthropic/claude-4-sonnet-20250101" {
		t.Fatalf("Primary = %q, want highest non-dot stable model", recommendation.Primary)
	}
}

func TestBestModelLexicalTieBreakerIsDeterministic(t *testing.T) {
	recommendation, ok := Recommend([]string{
		"vendor-b/claude-sonnet",
		"vendor-a/claude-sonnet",
	})
	if !ok {
		t.Fatalf("Recommend() ok = false, want true")
	}
	if recommendation.Primary != "vendor-a/claude-sonnet" {
		t.Fatalf("Primary = %q, want lexical first", recommendation.Primary)
	}
}

func TestRecommendationSetupValues(t *testing.T) {
	recommendation := Recommendation{
		Primary: "claude-sonnet",
		Haiku:   "claude-haiku",
		Sonnet:  "claude-sonnet",
		Opus:    "claude-opus",
	}
	values := recommendation.SetupValues("token", "https://gateway.example.com")
	if values.AuthToken != "token" || values.BaseURL != "https://gateway.example.com" {
		t.Fatalf("credentials = %q/%q, want token/base URL", values.AuthToken, values.BaseURL)
	}
	if values.Model != "claude-sonnet" || values.HaikuModel != "claude-haiku" || values.OpusModel != "claude-opus" {
		t.Fatalf("models = %+v, want recommendation mapping", values)
	}
}
