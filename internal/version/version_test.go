package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestInfoStringIncludesRuntimeTarget(t *testing.T) {
	info := Info{
		Version: "test-version",
		Commit:  "abc1234",
		Date:    "2026-05-12T00:00:00Z",
		Go:      "go-test",
		OS:      "testos",
		Arch:    "testarch",
	}

	got := info.String()

	for _, want := range []string{
		"llmgate test-version\n",
		"commit: abc1234\n",
		"date: 2026-05-12T00:00:00Z\n",
		"go: go-test\n",
		"platform: testos/testarch\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("version output missing %q in:\n%s", want, got)
		}
	}
}

func TestInfoStringDefaultsOptionalBuildFields(t *testing.T) {
	got := (Info{}).String()

	for _, want := range []string{
		"llmgate dev\n",
		"go: " + runtime.Version() + "\n",
		"platform: " + runtime.GOOS + "/" + runtime.GOARCH + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("version output missing %q in:\n%s", want, got)
		}
	}

	for _, notWant := range []string{"commit:", "date:"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("version output included empty optional field %q in:\n%s", notWant, got)
		}
	}
}

func TestCurrentFallsBackToDevVersion(t *testing.T) {
	originalVersion := version
	t.Cleanup(func() {
		version = originalVersion
	})

	version = ""

	if got := Current().Version; got != "dev" {
		t.Fatalf("Current().Version = %q, want dev", got)
	}
}
