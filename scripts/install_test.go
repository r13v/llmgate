package scripts

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallSHDryRunUsesOverrides(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is exercised on Unix-like platforms")
	}
	requireAnyTool(t, "sh")

	installDir := filepath.Join(t.TempDir(), "bin")
	cmd := exec.Command("sh", "scripts/install.sh", "--dry-run")
	cmd.Dir = repoRoot(t)
	cmd.Env = testEnv(map[string]string{
		"LLMGATE_OS":          "darwin",
		"LLMGATE_ARCH":        "arm64",
		"LLMGATE_INSTALL_DIR": installDir,
	})

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh dry-run failed: %v\n%s", err, output)
	}

	got := string(output)
	for _, want := range []string{
		"dry_run=1",
		"archive=llmgate-main-darwin-arm64.tar.gz",
		"install_dir=" + installDir,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("install.sh dry-run missing %q in:\n%s", want, got)
		}
	}
}

func TestInstallSHInstallsVerifiedLocalRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is exercised on Unix-like platforms")
	}
	requireAnyTool(t, "sh")
	requireAnyTool(t, "tar")
	requireAnyTool(t, "curl", "wget")

	archiveName := "llmgate-main-linux-amd64.tar.gz"
	binaryContent := []byte("fake llmgate\n")
	archiveData := tarGzWithFile(t, "llmgate", 0o755, binaryContent)
	server := fakeReleaseServer(t, map[string][]byte{
		archiveName:     archiveData,
		"checksums.txt": []byte(checksumLine(archiveName, archiveData)),
	})

	installDir := filepath.Join(t.TempDir(), "bin")
	cmd := exec.Command("sh", "scripts/install.sh")
	cmd.Dir = repoRoot(t)
	cmd.Env = testEnv(map[string]string{
		"LLMGATE_RELEASE_URL": server.URL,
		"LLMGATE_OS":          "linux",
		"LLMGATE_ARCH":        "amd64",
		"LLMGATE_INSTALL_DIR": installDir,
	})

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "llmgate installed to "+filepath.Join(installDir, "llmgate")) {
		t.Fatalf("install.sh output did not report install path:\n%s", output)
	}

	installed := filepath.Join(installDir, "llmgate")
	data, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if string(data) != string(binaryContent) {
		t.Fatalf("installed binary content = %q, want %q", data, binaryContent)
	}
	if info, err := os.Stat(installed); err != nil {
		t.Fatalf("stat installed binary: %v", err)
	} else if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed binary is not executable: %v", info.Mode().Perm())
	}
}

func TestInstallSHRejectsChecksumMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is exercised on Unix-like platforms")
	}
	requireAnyTool(t, "sh")
	requireAnyTool(t, "tar")
	requireAnyTool(t, "curl", "wget")

	archiveName := "llmgate-main-linux-amd64.tar.gz"
	archiveData := tarGzWithFile(t, "llmgate", 0o755, []byte("fake llmgate\n"))
	server := fakeReleaseServer(t, map[string][]byte{
		archiveName: archiveData,
		"checksums.txt": []byte(
			"0000000000000000000000000000000000000000000000000000000000000000  " + archiveName + "\n",
		),
	})

	installDir := filepath.Join(t.TempDir(), "bin")
	cmd := exec.Command("sh", "scripts/install.sh")
	cmd.Dir = repoRoot(t)
	cmd.Env = testEnv(map[string]string{
		"LLMGATE_RELEASE_URL": server.URL,
		"LLMGATE_OS":          "linux",
		"LLMGATE_ARCH":        "amd64",
		"LLMGATE_INSTALL_DIR": installDir,
	})

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install.sh succeeded with a bad checksum:\n%s", output)
	}
	if !strings.Contains(string(output), "checksum mismatch") {
		t.Fatalf("install.sh mismatch output missing checksum error:\n%s", output)
	}
	if _, statErr := os.Stat(filepath.Join(installDir, "llmgate")); !os.IsNotExist(statErr) {
		t.Fatalf("install.sh wrote binary despite checksum mismatch: %v", statErr)
	}
}

func TestInstallPS1DryRunUsesWindowsDefaults(t *testing.T) {
	ps, args := powershellCommand(t)

	localAppData := filepath.Join(t.TempDir(), "LocalAppData")
	args = append(args, "scripts/install.ps1", "-DryRun")
	cmd := exec.Command(ps, args...)
	cmd.Dir = repoRoot(t)
	cmd.Env = testEnv(map[string]string{
		"LLMGATE_OS":          "windows",
		"LLMGATE_ARCH":        "arm64",
		"LLMGATE_ADD_TO_PATH": "1",
		"LOCALAPPDATA":        localAppData,
	})

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.ps1 dry-run failed: %v\n%s", err, output)
	}

	got := string(output)
	for _, want := range []string{
		"dry_run=1",
		"archive=llmgate-main-windows-arm64.zip",
		"install_dir=" + filepath.Join(localAppData, "Programs", "llmgate", "bin"),
		"add_to_path=True",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("install.ps1 dry-run missing %q in:\n%s", want, got)
		}
	}
}

func TestInstallPS1InstallsVerifiedLocalRelease(t *testing.T) {
	ps, args := powershellCommand(t)

	archiveName := "llmgate-main-windows-amd64.zip"
	binaryContent := []byte("fake llmgate.exe\n")
	archiveData := zipWithFile(t, "llmgate.exe", binaryContent)
	server := fakeReleaseServer(t, map[string][]byte{
		archiveName:     archiveData,
		"checksums.txt": []byte(checksumLine(archiveName, archiveData)),
	})

	localAppData := filepath.Join(t.TempDir(), "LocalAppData")
	args = append(args, "scripts/install.ps1")
	cmd := exec.Command(ps, args...)
	cmd.Dir = repoRoot(t)
	cmd.Env = testEnv(map[string]string{
		"LLMGATE_RELEASE_URL": server.URL,
		"LLMGATE_OS":          "windows",
		"LLMGATE_ARCH":        "amd64",
		"LOCALAPPDATA":        localAppData,
	})

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.ps1 failed: %v\n%s", err, output)
	}

	installed := filepath.Join(localAppData, "Programs", "llmgate", "bin", "llmgate.exe")
	data, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("read installed binary: %v\n%s", err, output)
	}
	if string(data) != string(binaryContent) {
		t.Fatalf("installed binary content = %q, want %q", data, binaryContent)
	}
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

func fakeReleaseServer(t *testing.T, assets map[string][]byte) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		data, ok := assets[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	return server
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
	env := os.Environ()
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
