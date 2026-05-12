package ci

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCIWorkflowMatchesPlan(t *testing.T) {
	workflow := readRepoFile(t, ".github", "workflows", "ci.yml")

	for _, want := range []string{
		"actions/checkout@v6",
		"actions/setup-go@v6",
		`go-version: "1.26.3"`,
		"ubuntu-latest",
		"macos-latest",
		"windows-latest",
		"golangci/golangci-lint-action@v9",
		"version: v2.12.2",
		"make fmt",
		"make lint",
		"make test",
		"make test-e2e",
		"shellcheck scripts/install.sh",
		"scripts/install.ps1",
		"-DryRun",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("CI workflow missing %q", want)
		}
	}
}

func TestDependabotTracksGitHubActionsMajorTags(t *testing.T) {
	dependabot := readRepoFile(t, ".github", "dependabot.yml")

	for _, want := range []string{
		`package-ecosystem: "github-actions"`,
		`directory: "/"`,
		"github-actions-major-tags",
		`- "major"`,
	} {
		if !strings.Contains(dependabot, want) {
			t.Fatalf("dependabot config missing %q", want)
		}
	}
}

func TestReadmeDocumentsCI(t *testing.T) {
	readme := readRepoFile(t, "README.md")

	for _, want := range []string{
		"GitHub Actions",
		"Linux, macOS, and Windows",
		"make fmt",
		"make lint",
		"make test",
		"make test-e2e",
		"golangci-lint v2.12.2",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README missing CI detail %q", want)
		}
	}
}

func readRepoFile(t *testing.T, path ...string) string {
	t.Helper()

	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(append([]string{root}, path...)...))
	if err != nil {
		t.Fatalf("read repo file %s: %v", filepath.Join(path...), err)
	}
	return string(data)
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
