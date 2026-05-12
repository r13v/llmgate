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
		"--prerelease",
		"--target \"${GITHUB_SHA}\"",
		"Rolling main prerelease for commit ${COMMIT_SHA}.",
		"Installer scripts are attached as install.sh and install.ps1.",
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
		"also attaches `install.sh` and `install.ps1`",
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
		"install.sh -> " + filepath.Join(distDir, "install.sh"),
		"install.ps1 -> " + filepath.Join(distDir, "install.ps1"),
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
	assertDirEntries(t, distDir, append(expectedArchives, "checksums.txt", "install.sh", "install.ps1"))

	for _, name := range expectedArchives {
		assertArchiveContents(t, filepath.Join(distDir, name), strings.Contains(name, "windows"))
	}
	assertChecksums(t, distDir, expectedArchives)
}

func TestReadmeDocumentsInstallScripts(t *testing.T) {
	readme := readRepoFile(t, "README.md")

	for _, want := range []string{
		"curl -fsSL https://github.com/r13v/llmgate/releases/download/main/install.sh | sh",
		"iwr https://github.com/r13v/llmgate/releases/download/main/install.ps1 -UseB | iex",
		"LLMGATE_INSTALL_DIR",
		"LLMGATE_OS",
		"LLMGATE_ARCH",
		"LLMGATE_ADD_TO_PATH=1",
		"do not support SemVer version selection",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README missing installer detail %q", want)
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
	script := `#!/usr/bin/env sh
set -eu
out=""
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
