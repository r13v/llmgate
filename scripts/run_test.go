package scripts

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/xpty"
)

func TestRunSHDownloadsCachesAndForwardsArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("run.sh is exercised on Unix-like platforms")
	}
	requireAnyTool(t, "sh")
	requireAnyTool(t, "tar")
	requireAnyTool(t, "curl", "wget")

	archiveName := unixArchiveName(t)
	archiveData := tarGzWithFile(t, "llmgate", 0o755, fakeBinary(t, "v1", 0))
	release := newFakeRelease(t, map[string][]byte{
		archiveName:     archiveData,
		"checksums.txt": []byte(checksumLine(archiveName, archiveData)),
	})
	scriptPath := patchedRunScript(t, "run.sh", release.URL())
	cacheDir := filepath.Join(t.TempDir(), "cache")

	output, err := runSH(t, scriptPath, cacheDir, "--version", "extra")
	if err != nil {
		t.Fatalf("run.sh failed: %v\n%s", err, output)
	}

	got := string(output)
	for _, want := range []string{
		"Downloading llmgate...",
		"fake v1 args=--version,extra",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("run.sh output missing %q in:\n%s", want, got)
		}
	}

	if release.Count("checksums.txt") != 1 {
		t.Fatalf("checksums.txt downloads = %d, want 1", release.Count("checksums.txt"))
	}
	if release.Count(archiveName) != 1 {
		t.Fatalf("%s downloads = %d, want 1", archiveName, release.Count(archiveName))
	}
}

func TestRunSHReopensTTYForPipedNoArgWizard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("run.sh is exercised on Unix-like platforms")
	}
	requireAnyTool(t, "sh")
	requireAnyTool(t, "tar")
	requireAnyTool(t, "curl", "wget")

	archiveName := unixArchiveName(t)
	archiveData := tarGzWithFile(t, "llmgate", 0o755, []byte(`#!/usr/bin/env sh
if [ -t 0 ]; then
	printf '%s\n' 'fake wizard stdin is tty'
	exit 0
fi
printf '%s\n' 'fake wizard stdin is not tty'
exit 31
`))
	release := newFakeRelease(t, map[string][]byte{
		archiveName:     archiveData,
		"checksums.txt": []byte(checksumLine(archiveName, archiveData)),
	})
	scriptPath := patchedRunScript(t, "run.sh", release.URL())
	cacheDir := filepath.Join(t.TempDir(), "cache")

	output, err := runSHFromPipedScriptWithPTY(t, scriptPath, cacheDir)
	if err != nil {
		t.Fatalf("run.sh failed: %v\n%s", err, output)
	}

	got := string(output)
	if !strings.Contains(got, "fake wizard stdin is tty") {
		t.Fatalf("run.sh did not reopen the controlling terminal:\n%s", got)
	}
	if strings.Contains(got, "fake wizard stdin is not tty") {
		t.Fatalf("run.sh left script stdin attached to llmgate:\n%s", got)
	}
}

func TestRunSHCacheHitSkipsArchiveDownload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("run.sh is exercised on Unix-like platforms")
	}
	requireAnyTool(t, "sh")
	requireAnyTool(t, "tar")
	requireAnyTool(t, "curl", "wget")

	archiveName := unixArchiveName(t)
	archiveData := tarGzWithFile(t, "llmgate", 0o755, fakeBinary(t, "v1", 0))
	release := newFakeRelease(t, map[string][]byte{
		archiveName:     archiveData,
		"checksums.txt": []byte(checksumLine(archiveName, archiveData)),
	})
	scriptPath := patchedRunScript(t, "run.sh", release.URL())
	cacheDir := filepath.Join(t.TempDir(), "cache")

	if output, err := runSH(t, scriptPath, cacheDir); err != nil {
		t.Fatalf("first run.sh failed: %v\n%s", err, output)
	}

	output, err := runSH(t, scriptPath, cacheDir, "again")
	if err != nil {
		t.Fatalf("second run.sh failed: %v\n%s", err, output)
	}
	got := string(output)
	if strings.Contains(got, "Downloading llmgate") || strings.Contains(got, "Updating llmgate") {
		t.Fatalf("cache hit should be quiet before app output:\n%s", got)
	}
	if !strings.Contains(got, "fake v1 args=again") {
		t.Fatalf("cache hit did not run cached app with args:\n%s", got)
	}
	if release.Count("checksums.txt") != 2 {
		t.Fatalf("checksums.txt downloads = %d, want 2", release.Count("checksums.txt"))
	}
	if release.Count(archiveName) != 1 {
		t.Fatalf("%s downloads = %d, want 1", archiveName, release.Count(archiveName))
	}
}

func TestRunSHRejectsChecksumMismatchWithoutCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("run.sh is exercised on Unix-like platforms")
	}
	requireAnyTool(t, "sh")
	requireAnyTool(t, "tar")
	requireAnyTool(t, "curl", "wget")

	archiveName := unixArchiveName(t)
	archiveData := tarGzWithFile(t, "llmgate", 0o755, fakeBinary(t, "bad", 0))
	release := newFakeRelease(t, map[string][]byte{
		archiveName: archiveData,
		"checksums.txt": []byte(
			"0000000000000000000000000000000000000000000000000000000000000000  " + archiveName + "\n",
		),
	})
	scriptPath := patchedRunScript(t, "run.sh", release.URL())

	output, err := runSH(t, scriptPath, filepath.Join(t.TempDir(), "cache"))
	if err == nil {
		t.Fatalf("run.sh succeeded with a bad checksum:\n%s", output)
	}
	got := string(output)
	if !strings.Contains(got, "checksum mismatch") {
		t.Fatalf("run.sh mismatch output missing checksum error:\n%s", got)
	}
	if strings.Contains(got, "fake bad") {
		t.Fatalf("run.sh executed the app despite checksum mismatch:\n%s", got)
	}
}

func TestRunSHFallsBackToValidCacheWhenUpdateCheckFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("run.sh is exercised on Unix-like platforms")
	}
	requireAnyTool(t, "sh")
	requireAnyTool(t, "tar")
	requireAnyTool(t, "curl", "wget")

	archiveName := unixArchiveName(t)
	archiveData := tarGzWithFile(t, "llmgate", 0o755, fakeBinary(t, "v1", 0))
	release := newFakeRelease(t, map[string][]byte{
		archiveName:     archiveData,
		"checksums.txt": []byte(checksumLine(archiveName, archiveData)),
	})
	scriptPath := patchedRunScript(t, "run.sh", release.URL())
	cacheDir := filepath.Join(t.TempDir(), "cache")

	if output, err := runSH(t, scriptPath, cacheDir); err != nil {
		t.Fatalf("first run.sh failed: %v\n%s", err, output)
	}
	release.Close()

	output, err := runSH(t, scriptPath, cacheDir, "offline")
	if err != nil {
		t.Fatalf("run.sh did not fall back to cache: %v\n%s", err, output)
	}
	got := string(output)
	for _, want := range []string{
		"Could not check for updates; running cached llmgate.",
		"fake v1 args=offline",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("run.sh fallback output missing %q in:\n%s", want, got)
		}
	}
}

func TestRunSHFallsBackToValidCacheWhenUpdateDownloadFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("run.sh is exercised on Unix-like platforms")
	}
	requireAnyTool(t, "sh")
	requireAnyTool(t, "tar")
	requireAnyTool(t, "curl", "wget")

	archiveName := unixArchiveName(t)
	v1ArchiveData := tarGzWithFile(t, "llmgate", 0o755, fakeBinary(t, "v1", 0))
	v2ArchiveData := tarGzWithFile(t, "llmgate", 0o755, fakeBinary(t, "v2", 0))
	release := newFakeRelease(t, map[string][]byte{
		archiveName:     v1ArchiveData,
		"checksums.txt": []byte(checksumLine(archiveName, v1ArchiveData)),
	})
	scriptPath := patchedRunScript(t, "run.sh", release.URL())
	cacheDir := filepath.Join(t.TempDir(), "cache")

	if output, err := runSH(t, scriptPath, cacheDir); err != nil {
		t.Fatalf("first run.sh failed: %v\n%s", err, output)
	}

	release.SetAssets(map[string][]byte{
		"checksums.txt": []byte(checksumLine(archiveName, v2ArchiveData)),
	})

	output, err := runSH(t, scriptPath, cacheDir, "stale-ok")
	if err != nil {
		t.Fatalf("run.sh did not fall back after update failure: %v\n%s", err, output)
	}
	got := string(output)
	for _, want := range []string{
		"Updating llmgate...",
		"Could not update llmgate; running cached llmgate.",
		"fake v1 args=stale-ok",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("run.sh update fallback output missing %q in:\n%s", want, got)
		}
	}
}

func TestRunSHPreservesAppExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("run.sh is exercised on Unix-like platforms")
	}
	requireAnyTool(t, "sh")
	requireAnyTool(t, "tar")
	requireAnyTool(t, "curl", "wget")

	archiveName := unixArchiveName(t)
	archiveData := tarGzWithFile(t, "llmgate", 0o755, fakeBinary(t, "exit", 23))
	release := newFakeRelease(t, map[string][]byte{
		archiveName:     archiveData,
		"checksums.txt": []byte(checksumLine(archiveName, archiveData)),
	})
	scriptPath := patchedRunScript(t, "run.sh", release.URL())

	output, err := runSH(t, scriptPath, filepath.Join(t.TempDir(), "cache"), "code")
	if err == nil {
		t.Fatalf("run.sh succeeded despite app failure:\n%s", output)
	}
	assertExitCode(t, err, 23)
	if !strings.Contains(string(output), "fake exit args=code") {
		t.Fatalf("run.sh did not execute app before preserving exit code:\n%s", output)
	}
}

func TestRunPS1DownloadsCachesAndForwardsArgs(t *testing.T) {
	ps, psArgs := powershellCommand(t)

	archiveName := windowsArchiveName(t)
	archiveData := zipWithFile(t, "llmgate.exe", fakeBinary(t, "ps-v1", 0))
	release := newFakeRelease(t, map[string][]byte{
		archiveName:     archiveData,
		"checksums.txt": []byte(checksumLine(archiveName, archiveData)),
	})
	scriptPath := patchedRunScript(t, "run.ps1", release.URL())
	localAppData := filepath.Join(t.TempDir(), "LocalAppData")

	output, err := runPS1(t, ps, psArgs, scriptPath, localAppData, "--version")
	if err != nil {
		t.Fatalf("run.ps1 failed: %v\n%s", err, output)
	}

	got := string(output)
	for _, want := range []string{
		"Downloading llmgate...",
		"fake ps-v1 args=--version",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("run.ps1 output missing %q in:\n%s", want, got)
		}
	}
}

func TestRunPS1RejectsChecksumMismatchWithoutCache(t *testing.T) {
	ps, psArgs := powershellCommand(t)

	archiveName := windowsArchiveName(t)
	archiveData := zipWithFile(t, "llmgate.exe", fakeBinary(t, "ps-bad", 0))
	release := newFakeRelease(t, map[string][]byte{
		archiveName: archiveData,
		"checksums.txt": []byte(
			"0000000000000000000000000000000000000000000000000000000000000000  " + archiveName + "\n",
		),
	})
	scriptPath := patchedRunScript(t, "run.ps1", release.URL())

	output, err := runPS1(t, ps, psArgs, scriptPath, filepath.Join(t.TempDir(), "LocalAppData"))
	if err == nil {
		t.Fatalf("run.ps1 succeeded with a bad checksum:\n%s", output)
	}
	got := string(output)
	if !strings.Contains(got, "checksum mismatch") {
		t.Fatalf("run.ps1 mismatch output missing checksum error:\n%s", got)
	}
	if strings.Contains(got, "fake ps-bad") {
		t.Fatalf("run.ps1 executed the app despite checksum mismatch:\n%s", got)
	}
}

func TestRunPS1FallsBackToValidCacheWhenUpdateCheckFails(t *testing.T) {
	ps, psArgs := powershellCommand(t)

	archiveName := windowsArchiveName(t)
	archiveData := zipWithFile(t, "llmgate.exe", fakeBinary(t, "ps-v1", 0))
	release := newFakeRelease(t, map[string][]byte{
		archiveName:     archiveData,
		"checksums.txt": []byte(checksumLine(archiveName, archiveData)),
	})
	scriptPath := patchedRunScript(t, "run.ps1", release.URL())
	localAppData := filepath.Join(t.TempDir(), "LocalAppData")

	if output, err := runPS1(t, ps, psArgs, scriptPath, localAppData); err != nil {
		t.Fatalf("first run.ps1 failed: %v\n%s", err, output)
	}
	release.Close()

	output, err := runPS1(t, ps, psArgs, scriptPath, localAppData, "offline")
	if err != nil {
		t.Fatalf("run.ps1 did not fall back to cache: %v\n%s", err, output)
	}
	got := string(output)
	for _, want := range []string{
		"Could not check for updates; running cached llmgate.",
		"fake ps-v1 args=offline",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("run.ps1 fallback output missing %q in:\n%s", want, got)
		}
	}
}

func TestRunPS1PreservesAppExitCode(t *testing.T) {
	ps, psArgs := powershellCommand(t)

	archiveName := windowsArchiveName(t)
	archiveData := zipWithFile(t, "llmgate.exe", fakeBinary(t, "ps-exit", 17))
	release := newFakeRelease(t, map[string][]byte{
		archiveName:     archiveData,
		"checksums.txt": []byte(checksumLine(archiveName, archiveData)),
	})
	scriptPath := patchedRunScript(t, "run.ps1", release.URL())

	output, err := runPS1(t, ps, psArgs, scriptPath, filepath.Join(t.TempDir(), "LocalAppData"), "code")
	if err == nil {
		t.Fatalf("run.ps1 succeeded despite app failure:\n%s", output)
	}
	assertExitCode(t, err, 17)
	if !strings.Contains(string(output), "fake ps-exit args=code") {
		t.Fatalf("run.ps1 did not execute app before preserving exit code:\n%s", output)
	}
}

func runSH(t *testing.T, scriptPath, cacheDir string, appArgs ...string) ([]byte, error) {
	t.Helper()

	args := append([]string{scriptPath}, appArgs...)
	cmd := exec.Command("sh", args...)
	cmd.Env = testEnv(map[string]string{
		"XDG_CACHE_HOME": cacheDir,
		"HOME":           filepath.Join(t.TempDir(), "home"),
	})
	return cmd.CombinedOutput()
}

func runSHFromPipedScriptWithPTY(t *testing.T, scriptPath, cacheDir string) ([]byte, error) {
	t.Helper()

	pty, err := xpty.NewUnixPty(80, 24)
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	defer func() {
		_ = pty.Close()
	}()

	cmd := exec.Command("sh", "-c", `exec sh < "$1"`, "llmgate-run-test", scriptPath)
	cmd.Env = testEnv(map[string]string{
		"XDG_CACHE_HOME": cacheDir,
		"HOME":           filepath.Join(t.TempDir(), "home"),
	})
	cmd.SysProcAttr = pipedScriptSysProcAttr()

	var output bytes.Buffer
	readDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&output, pty)
		readDone <- copyErr
	}()

	if err := pty.Start(cmd); err != nil {
		return output.Bytes(), err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitDone:
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		waitErr = ctx.Err()
		<-waitDone
	}
	_ = pty.Close()

	select {
	case <-readDone:
	case <-time.After(time.Second):
	}
	return output.Bytes(), waitErr
}

func runPS1(t *testing.T, ps string, psArgs []string, scriptPath, localAppData string, appArgs ...string) ([]byte, error) {
	t.Helper()

	args := append(append([]string{}, psArgs...), scriptPath)
	args = append(args, appArgs...)
	cmd := exec.Command(ps, args...)
	cmd.Env = testEnv(map[string]string{
		"LOCALAPPDATA": localAppData,
	})
	return cmd.CombinedOutput()
}

func assertExitCode(t *testing.T, err error, want int) {
	t.Helper()

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("error is %T, want *exec.ExitError: %v", err, err)
	}
	if got := exitErr.ExitCode(); got != want {
		t.Fatalf("exit code = %d, want %d", got, want)
	}
}

func patchedRunScript(t *testing.T, scriptName, releaseURL string) string {
	t.Helper()

	path := filepath.Join(repoRoot(t), "scripts", scriptName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", scriptName, err)
	}

	content := string(data)
	switch scriptName {
	case "run.sh":
		content = strings.Replace(content,
			`RELEASE_URL="https://github.com/r13v/llmgate/releases/download/main"`,
			`RELEASE_URL="`+releaseURL+`"`,
			1,
		)
	case "run.ps1":
		content = strings.Replace(content,
			`$ReleaseUrl = "https://github.com/r13v/llmgate/releases/download/main"`,
			`$ReleaseUrl = "`+releaseURL+`"`,
			1,
		)
	default:
		t.Fatalf("unsupported script: %s", scriptName)
	}
	if content == string(data) {
		t.Fatalf("release URL was not patched in %s", scriptName)
	}

	outPath := filepath.Join(t.TempDir(), scriptName)
	if err := os.WriteFile(outPath, []byte(content), 0o755); err != nil {
		t.Fatalf("write patched %s: %v", scriptName, err)
	}
	return outPath
}

func fakeBinary(t *testing.T, label string, exitCode int) []byte {
	t.Helper()
	requireAnyTool(t, "go")

	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "main.go")
	binaryPath := filepath.Join(dir, "fake-llmgate")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}

	source := fmt.Sprintf(`package main

import (
	"fmt"
	"os"
	"strings"
)

const label = %q
const exitCode = %d

func main() {
	fmt.Printf("fake %%s args=%%s\n", label, strings.Join(os.Args[1:], ","))
	os.Exit(exitCode)
}
`, label, exitCode)
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatalf("write fake binary source: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", binaryPath, sourcePath)
	cmd.Env = testEnv(map[string]string{"CGO_ENABLED": "0"})
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake binary: %v\n%s", err, output)
	}

	data, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read fake binary: %v", err)
	}
	return data
}

func unixArchiveName(t *testing.T) string {
	t.Helper()

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("unsupported Unix test OS: %s", runtime.GOOS)
	}
	return fmt.Sprintf("llmgate-main-%s-%s.tar.gz", runtime.GOOS, releaseArch(t))
}

func windowsArchiveName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("llmgate-main-windows-%s.zip", releaseArch(t))
}

func releaseArch(t *testing.T) string {
	t.Helper()

	switch runtime.GOARCH {
	case "amd64", "arm64":
		return runtime.GOARCH
	default:
		t.Skipf("unsupported test architecture: %s", runtime.GOARCH)
		return ""
	}
}

type fakeRelease struct {
	server    *httptest.Server
	closeOnce sync.Once
	mu        sync.Mutex
	assets    map[string][]byte
	counts    map[string]int
}

func newFakeRelease(t *testing.T, assets map[string][]byte) *fakeRelease {
	t.Helper()

	release := &fakeRelease{
		assets: assets,
		counts: map[string]int{},
	}
	release.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		release.mu.Lock()
		release.counts[name]++
		data, ok := release.assets[name]
		release.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(release.Close)
	return release
}

func (r *fakeRelease) URL() string {
	return r.server.URL
}

func (r *fakeRelease) SetAssets(assets map[string][]byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.assets = assets
}

func (r *fakeRelease) Count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[name]
}

func (r *fakeRelease) Close() {
	r.closeOnce.Do(r.server.Close)
}

func powershellCommand(t *testing.T) (string, []string) {
	t.Helper()

	for _, name := range []string{"pwsh", "powershell"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		args := []string{"-NoProfile", "-NonInteractive"}
		if runtime.GOOS == "windows" {
			args = append(args, "-ExecutionPolicy", "Bypass")
		}
		args = append(args, "-File")
		return path, args
	}
	t.Skip("PowerShell is not available")
	return "", nil
}

func tarGzWithFile(t *testing.T, name string, mode int64, content []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzipWriter)

	header := &tar.Header{
		Name: name,
		Mode: mode,
		Size: int64(len(content)),
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tarWriter.Write(content); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func zipWithFile(t *testing.T, name string, content []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)
	writer, err := zipWriter.Create(name)
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatalf("write zip body: %v", err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func checksumLine(name string, data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) + "  " + name + "\n"
}

func requireAnyTool(t *testing.T, names ...string) {
	t.Helper()

	for _, name := range names {
		if _, err := exec.LookPath(name); err == nil {
			return
		}
	}
	t.Skipf("none of these tools are available: %s", strings.Join(names, ", "))
}

func testEnv(overrides map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, item := range os.Environ() {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, overridden := overrides[name]; overridden {
			continue
		}
		env = append(env, item)
	}
	for name, value := range overrides {
		env = append(env, name+"="+value)
	}
	return env
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}
