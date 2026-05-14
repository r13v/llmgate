package ci

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
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
		"runner.os == 'Linux'",
		"golangci/golangci-lint-action@v9",
		"version: v2.12.2",
		"make fmt",
		"go test ./...",
		"go test -tags=e2e ./...",
		"shellcheck scripts/run.sh",
		"scripts/run.ps1",
		"[scriptblock]::Create",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("CI workflow missing %q", want)
		}
	}

	for _, unwanted := range []string{
		"choco install make",
		"Run Makefile lint target",
	} {
		if strings.Contains(workflow, unwanted) {
			t.Fatalf("CI workflow should not contain slow duplicate step %q", unwanted)
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
		"Linux runs formatting and linting",
		"`go test ./...`",
		"`go test -tags=e2e ./...`",
		"golangci-lint v2.12.2",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README missing CI detail %q", want)
		}
	}
}

func TestReleaseWorkflowMatchesPlan(t *testing.T) {
	workflow := readRepoFile(t, ".github", "workflows", "release-main.yml")

	for _, want := range []string{
		"actions/checkout@v6",
		"actions/setup-go@v6",
		`go-version: "1.26.3"`,
		"contents: write",
		"make check",
		"make package",
		"VERSION: main",
		"COMMIT: ${{ github.sha }}",
		"gh release create main",
		"gh release edit main",
		"gh release upload main dist/* --clobber",
		"GITHUB_TOKEN: ${{ github.token }}",
		"--draft=false",
		"--prerelease",
		"--target \"${GITHUB_SHA}\"",
		"Rolling main prerelease for commit ${COMMIT_SHA}.",
		"Run scripts are attached as run.sh and run.ps1.",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("release workflow missing %q", want)
		}
	}
}

func TestMakefileExposesPackageTarget(t *testing.T) {
	makefile := readRepoFile(t, "Makefile")

	for _, want := range []string{
		"package:",
		"VERSION=main",
		"scripts/package.sh",
	} {
		if !strings.Contains(makefile, want) {
			t.Fatalf("Makefile missing package detail %q", want)
		}
	}
}

func TestReadmeDocumentsRollingMainRelease(t *testing.T) {
	readme := readRepoFile(t, "README.md")

	for _, want := range []string{
		"Rolling Main Release",
		"rolling prerelease named `main`",
		"llmgate-main-linux-amd64.tar.gz",
		"llmgate-main-windows-arm64.zip",
		"`checksums.txt` contains SHA-256 digests",
		"also attaches `run.sh` and `run.ps1`",
		"Release notes include",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README missing release detail %q", want)
		}
	}
}

func TestPackageScriptDryRunListsReleaseTargets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripts/package.sh is exercised on Unix-like platforms")
	}
	requireTool(t, "bash")

	distDir := filepath.Join(t.TempDir(), "dist")
	cmd := exec.Command("bash", "scripts/package.sh", "--dry-run", "--dist", distDir)
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(),
		"VERSION=main",
		"COMMIT=abc123",
		"DATE=2026-05-12T00:00:00Z",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("package dry-run failed: %v\n%s", err, output)
	}

	got := string(output)
	for _, want := range []string{
		"version=main",
		"commit=abc123",
		"date=2026-05-12T00:00:00Z",
		"linux-amd64 -> " + filepath.Join(distDir, "llmgate-main-linux-amd64.tar.gz"),
		"linux-arm64 -> " + filepath.Join(distDir, "llmgate-main-linux-arm64.tar.gz"),
		"darwin-amd64 -> " + filepath.Join(distDir, "llmgate-main-darwin-amd64.tar.gz"),
		"darwin-arm64 -> " + filepath.Join(distDir, "llmgate-main-darwin-arm64.tar.gz"),
		"windows-amd64 -> " + filepath.Join(distDir, "llmgate-main-windows-amd64.zip"),
		"windows-arm64 -> " + filepath.Join(distDir, "llmgate-main-windows-arm64.zip"),
		"checksums -> " + filepath.Join(distDir, "checksums.txt"),
		"run.sh -> " + filepath.Join(distDir, "run.sh"),
		"run.ps1 -> " + filepath.Join(distDir, "run.ps1"),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("package dry-run missing %q in:\n%s", want, got)
		}
	}
}

func TestPackageScriptCreatesArchivesAndChecksums(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripts/package.sh is exercised on Unix-like platforms")
	}
	requireTool(t, "bash")
	requireTool(t, "tar")
	requireTool(t, "zip")

	tempDir := t.TempDir()
	distDir := filepath.Join(tempDir, "dist")
	fakeGo := writeFakeGo(t, tempDir)

	cmd := exec.Command("bash", "scripts/package.sh", "--dist", distDir)
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(),
		"GO="+fakeGo,
		"VERSION=main",
		"COMMIT=abc123",
		"DATE=2026-05-12T00:00:00Z",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("package script failed: %v\n%s", err, output)
	}

	expectedArchives := []string{
		"llmgate-main-darwin-amd64.tar.gz",
		"llmgate-main-darwin-arm64.tar.gz",
		"llmgate-main-linux-amd64.tar.gz",
		"llmgate-main-linux-arm64.tar.gz",
		"llmgate-main-windows-amd64.zip",
		"llmgate-main-windows-arm64.zip",
	}
	assertDirEntries(t, distDir, append(expectedArchives, "checksums.txt", "run.sh", "run.ps1"))
	assertFakeGoBuilds(t, filepath.Join(tempDir, "fake-go.log"))

	for _, name := range expectedArchives {
		assertArchiveContents(t, filepath.Join(distDir, name), strings.Contains(name, "windows"))
	}
	assertChecksums(t, distDir, expectedArchives)
}

func TestReadmeDocumentsRunScripts(t *testing.T) {
	readme := readRepoFile(t, "README.md")

	for _, want := range []string{
		"curl -fsSL https://github.com/r13v/llmgate/releases/download/main/run.sh | sh",
		"iwr https://github.com/r13v/llmgate/releases/download/main/run.ps1 -UseB | iex",
		"Run Script Details",
		"cache the verified binary",
		"If the update check fails",
		"The scripts forward arguments to `llmgate`",
		"curl -fsSL https://github.com/r13v/llmgate/releases/download/main/run.sh | sh -s -- --version",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README missing run-script detail %q", want)
		}
	}

	for _, removed := range []string{
		"LLMGATE_INSTALL_DIR",
		"LLMGATE_OS",
		"LLMGATE_ARCH",
		"LLMGATE_ADD_TO_PATH",
		"install.sh | sh",
		"install.ps1 -UseB",
	} {
		if strings.Contains(readme, removed) {
			t.Fatalf("README still contains removed installer detail %q", removed)
		}
	}
}

func TestReadmeDocumentsFinalUserContract(t *testing.T) {
	readme := readRepoFile(t, "README.md")

	for _, want := range []string{
		"Before startup approval",
		"does not read files, check file existence, inspect",
		"environment variables, run local commands, make HTTP requests, or write",
		"does not use telemetry and does not write file logs",
		"Secrets are masked",
		"Linux amd64 and arm64",
		"macOS amd64 and arm64",
		"Windows amd64 and arm64",
		"Claude Code user settings at `~/.claude/settings.json`",
		"zsh, bash, or fish shell profile assignments",
		"Windows User environment variables",
		"VS Code user settings",
		"Cursor user settings",
		"Project settings under `./.claude/settings.local.json`",
		"Diagnostic status severity is `OK < SKIP < WARN < FAIL`",
		"`make check` runs formatting, linting, default tests",
		"e2e-tagged acceptance suite",
		"should be treated as prerelease",
		"artifacts, not stable versioned releases",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README missing final user-contract detail %q", want)
		}
	}
}

func TestProjectSpecClarifiesLegacyManagedBlocksOutOfScope(t *testing.T) {
	spec := readRepoFile(t, "docs", "PROJECT_SPEC.md")

	for _, want := range []string{
		"do not create, detect, rewrite, or treat legacy managed shell blocks as special",
		"only active line-based managed assignments participate in shell profile behavior",
	} {
		if !strings.Contains(spec, want) {
			t.Fatalf("project spec missing legacy-block clarification %q", want)
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

func requireTool(t *testing.T, name string) {
	t.Helper()

	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not available", name)
	}
}

func writeFakeGo(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "fake-go")
	logPath := filepath.Join(dir, "fake-go.log")
	script := `#!/usr/bin/env sh
set -eu
out=""
{
	echo "BEGIN"
	echo "GOOS=${GOOS:-}"
	echo "GOARCH=${GOARCH:-}"
	echo "CGO_ENABLED=${CGO_ENABLED:-}"
	for arg in "$@"; do
		echo "ARG=$arg"
	done
	echo "END"
} >> "` + logPath + `"
while [ "$#" -gt 0 ]; do
	case "$1" in
		-o)
			out="$2"
			shift 2
			;;
		*)
			shift
			;;
	esac
done
if [ -z "$out" ]; then
	echo "missing -o output path" >&2
	exit 1
fi
mkdir -p "$(dirname "$out")"
printf 'fake llmgate for %s/%s\n' "${GOOS:-unknown}" "${GOARCH:-unknown}" > "$out"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	return path
}

type fakeGoBuild struct {
	goos       string
	goarch     string
	cgoEnabled string
	args       []string
}

func assertFakeGoBuilds(t *testing.T, logPath string) {
	t.Helper()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake go log: %v", err)
	}
	builds := parseFakeGoBuilds(t, string(data))
	if len(builds) != 6 {
		t.Fatalf("fake go build count = %d, want 6:\n%s", len(builds), data)
	}

	expected := map[string]bool{
		"linux/amd64":   false,
		"linux/arm64":   false,
		"darwin/amd64":  false,
		"darwin/arm64":  false,
		"windows/amd64": false,
		"windows/arm64": false,
	}
	for _, build := range builds {
		key := build.goos + "/" + build.goarch
		if _, ok := expected[key]; !ok {
			t.Fatalf("unexpected fake go target %s in %#v", key, build)
		}
		expected[key] = true
		if build.cgoEnabled != "0" {
			t.Fatalf("%s CGO_ENABLED = %q, want 0", key, build.cgoEnabled)
		}
		args := strings.Join(build.args, "\n")
		for _, want := range []string{
			"build",
			"-trimpath",
			"-ldflags",
			"github.com/r13v/llmgate/internal/version.version=main",
			"github.com/r13v/llmgate/internal/version.commit=abc123",
			"github.com/r13v/llmgate/internal/version.date=2026-05-12T00:00:00Z",
			"./cmd/llmgate",
		} {
			if !strings.Contains(args, want) {
				t.Fatalf("%s fake go args missing %q:\n%s", key, want, args)
			}
		}
	}
	for target, seen := range expected {
		if !seen {
			t.Fatalf("fake go target %s was not built", target)
		}
	}
}

func parseFakeGoBuilds(t *testing.T, data string) []fakeGoBuild {
	t.Helper()

	var builds []fakeGoBuild
	var current *fakeGoBuild
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		switch {
		case line == "BEGIN":
			if current != nil {
				t.Fatalf("nested fake go log record:\n%s", data)
			}
			current = &fakeGoBuild{}
		case line == "END":
			if current == nil {
				t.Fatalf("fake go log END without BEGIN:\n%s", data)
			}
			builds = append(builds, *current)
			current = nil
		case strings.HasPrefix(line, "GOOS="):
			current.goos = strings.TrimPrefix(line, "GOOS=")
		case strings.HasPrefix(line, "GOARCH="):
			current.goarch = strings.TrimPrefix(line, "GOARCH=")
		case strings.HasPrefix(line, "CGO_ENABLED="):
			current.cgoEnabled = strings.TrimPrefix(line, "CGO_ENABLED=")
		case strings.HasPrefix(line, "ARG="):
			current.args = append(current.args, strings.TrimPrefix(line, "ARG="))
		default:
			t.Fatalf("unexpected fake go log line %q in:\n%s", line, data)
		}
	}
	if current != nil {
		t.Fatalf("unterminated fake go log record:\n%s", data)
	}
	return builds
}

func assertDirEntries(t *testing.T, dir string, expected []string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dist dir: %v", err)
	}

	var got []string
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	sort.Strings(got)
	sort.Strings(expected)

	if strings.Join(got, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("dist entries:\n got: %v\nwant: %v", got, expected)
	}
}

func assertArchiveContents(t *testing.T, path string, windows bool) {
	t.Helper()

	wantBinary := "llmgate"
	if windows {
		wantBinary = "llmgate.exe"
	}
	want := []string{"LICENSE", "README.md", wantBinary}
	got := archiveNames(t, path)
	sort.Strings(got)
	sort.Strings(want)

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s entries:\n got: %v\nwant: %v", filepath.Base(path), got, want)
	}
}

func archiveNames(t *testing.T, path string) []string {
	t.Helper()

	if strings.HasSuffix(path, ".zip") {
		return zipNames(t, path)
	}
	return tarGzNames(t, path)
}

func zipNames(t *testing.T, path string) []string {
	t.Helper()

	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip %s: %v", path, err)
	}
	defer func() {
		_ = reader.Close()
	}()

	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
	}
	return names
}

func tarGzNames(t *testing.T, path string) []string {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open tar.gz %s: %v", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("open gzip %s: %v", path, err)
	}
	defer func() {
		_ = gzipReader.Close()
	}()

	var names []string
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read tar.gz %s: %v", path, err)
		}
		names = append(names, header.Name)
	}
	return names
}

func assertChecksums(t *testing.T, distDir string, expectedArchives []string) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(distDir, "checksums.txt"))
	if err != nil {
		t.Fatalf("read checksums: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(expectedArchives) {
		t.Fatalf("checksum line count = %d, want %d:\n%s", len(lines), len(expectedArchives), data)
	}

	wantNames := make(map[string]struct{}, len(expectedArchives))
	for _, name := range expectedArchives {
		wantNames[name] = struct{}{}
	}

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("invalid checksum line %q", line)
		}
		gotDigest, name := fields[0], fields[1]
		if _, ok := wantNames[name]; !ok {
			t.Fatalf("unexpected checksum archive %q", name)
		}
		wantDigest := fileSHA256(t, filepath.Join(distDir, name))
		if gotDigest != wantDigest {
			t.Fatalf("%s digest = %s, want %s", name, gotDigest, wantDigest)
		}
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
