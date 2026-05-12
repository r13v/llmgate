package gateway

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/r13v/llmgate/internal/core"
)

var numberPattern = regexp.MustCompile(`\d+`)

type Recommendation struct {
	Primary string
	Haiku   string
	Sonnet  string
	Opus    string
}

func Recommend(models []string) (Recommendation, bool) {
	haiku := bestTierModel(models, "haiku")
	sonnet := bestTierModel(models, "sonnet")
	opus := bestTierModel(models, "opus")

	primary := sonnet
	if primary == "" {
		primary = opus
	}
	if primary == "" {
		primary = haiku
	}
	if primary == "" {
		return Recommendation{}, false
	}

	recommendation := Recommendation{
		Primary: primary,
		Haiku:   fallbackModel(haiku, primary),
		Sonnet:  fallbackModel(sonnet, primary),
		Opus:    fallbackModel(opus, primary),
	}
	return recommendation, true
}

func (r Recommendation) SetupValues(token, baseURL string) core.SetupValues {
	return core.SetupValues{
		AuthToken:   token,
		BaseURL:     baseURL,
		Model:       r.Primary,
		HaikuModel:  r.Haiku,
		SonnetModel: r.Sonnet,
		OpusModel:   r.Opus,
	}
}

func bestTierModel(models []string, tier string) string {
	candidates := make([]string, 0, len(models))
	for _, model := range models {
		if modelMatchesTier(model, tier) {
			candidates = append(candidates, model)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return betterModel(candidates[i], candidates[j])
	})
	return candidates[0]
}

func modelMatchesTier(model, tier string) bool {
	lower := strings.ToLower(model)
	return strings.Contains(lower, "claude") && strings.Contains(lower, tier)
}

func betterModel(left, right string) bool {
	leftHasDot := strings.Contains(left, ".")
	rightHasDot := strings.Contains(right, ".")
	if leftHasDot != rightHasDot {
		return !leftHasDot
	}

	if comparison := compareNumberComponents(extractNumbers(left), extractNumbers(right)); comparison != 0 {
		return comparison > 0
	}

	leftPrerelease := containsPrereleaseMarker(left)
	rightPrerelease := containsPrereleaseMarker(right)
	if leftPrerelease != rightPrerelease {
		return !leftPrerelease
	}

	leftKey := strings.ToLower(left)
	rightKey := strings.ToLower(right)
	if leftKey != rightKey {
		return leftKey < rightKey
	}
	return left < right
}

func extractNumbers(value string) []int {
	matches := numberPattern.FindAllString(value, -1)
	numbers := make([]int, 0, len(matches))
	for _, match := range matches {
		number, err := strconv.Atoi(match)
		if err == nil {
			numbers = append(numbers, number)
		}
	}
	return numbers
}

func compareNumberComponents(left, right []int) int {
	maxLength := len(left)
	if len(right) > maxLength {
		maxLength = len(right)
	}

	for i := 0; i < maxLength; i++ {
		leftValue := 0
		if i < len(left) {
			leftValue = left[i]
		}
		rightValue := 0
		if i < len(right) {
			rightValue = right[i]
		}
		if leftValue > rightValue {
			return 1
		}
		if leftValue < rightValue {
			return -1
		}
	}
	return 0
}

func containsPrereleaseMarker(model string) bool {
	lower := strings.ToLower(model)
	return strings.Contains(lower, "preview") ||
		strings.Contains(lower, "beta") ||
		strings.Contains(lower, "experimental")
}

func fallbackModel(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
