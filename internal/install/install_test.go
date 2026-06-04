package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResolvePathsUsesCanonicalUserLocalLocations(t *testing.T) {
	unixPaths, err := ResolvePaths(Options{
		GOOS:    "linux",
		HomeDir: "/home/ada",
	})
	if err != nil {
		t.Fatalf("ResolvePaths(linux) error = %v", err)
	}
	if unixPaths.InstallPath != "/home/ada/.local/bin/llmgate" {
		t.Fatalf("linux install path = %q", unixPaths.InstallPath)
	}
	if unixPaths.MetadataPath != "/home/ada/.local/state/llmgate/install.json" {
		t.Fatalf("linux metadata path = %q", unixPaths.MetadataPath)
	}

	windowsPaths, err := ResolvePaths(Options{
		GOOS:         "windows",
		HomeDir:      `C:\Users\Ada`,
		LocalAppData: `C:\Users\Ada\AppData\Local`,
	})
	if err != nil {
		t.Fatalf("ResolvePaths(windows) error = %v", err)
	}
	if windowsPaths.InstallPath != `C:\Users\Ada\AppData\Local\Programs\llmgate\llmgate.exe` {
		t.Fatalf("windows install path = %q", windowsPaths.InstallPath)
	}
	if windowsPaths.MetadataPath != `C:\Users\Ada\AppData\Local\llmgate\install.json` {
		t.Fatalf("windows metadata path = %q", windowsPaths.MetadataPath)
	}
}

func TestUpdateRejectsNonCanonicalExecutable(t *testing.T) {
	opts := testOptions(t)
	opts.Executable = filepath.Join(t.TempDir(), "llmgate")

	_, err := Update(context.Background(), opts)
	if err == nil {
		t.Fatal("Update() error = nil, want usage error")
	}
	if !IsUsageError(err) {
		t.Fatalf("Update() error = %T, want usage error: %v", err, err)
	}
	if !strings.Contains(err.Error(), "must be run from") {
		t.Fatalf("Update() error missing canonical message: %v", err)
	}
}

func TestUpdateRejectsExternallyModifiedInstall(t *testing.T) {
	opts := testOptions(t)
	paths := createOwnedInstall(t, opts, []byte("old binary"), strings.Repeat("a", 64))
	opts.Executable = paths.InstallPath
	if err := os.WriteFile(paths.InstallPath, []byte("modified binary"), 0o755); err != nil {
		t.Fatalf("modify install: %v", err)
	}

	_, err := Update(context.Background(), opts)
	if err == nil {
		t.Fatal("Update() error = nil, want usage error")
	}
	if !IsUsageError(err) {
		t.Fatalf("Update() error = %T, want usage error: %v", err, err)
	}
	if !strings.Contains(err.Error(), "changed outside llmgate") {
		t.Fatalf("Update() error missing ownership message: %v", err)
	}
}

func TestUpdateAlreadyCurrentSkipsArchiveDownload(t *testing.T) {
	opts := testOptions(t)
	archiveName := testArchiveName(t)
	archiveData := testArchive(t, []byte("new binary"))
	archiveSHA := checksumHex(archiveData)
	release := newInstallFakeRelease(t, map[string][]byte{
		"checksums.txt": []byte(archiveSHA + "  " + archiveName + "\n"),
	})
	opts.ReleaseURL = release.URL()
	paths := createOwnedInstall(t, opts, []byte("current binary"), archiveSHA)
	opts.Executable = paths.InstallPath

	result, err := Update(context.Background(), opts)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !result.AlreadyCurrent {
		t.Fatalf("AlreadyCurrent = false, want true")
	}
	if release.Count("checksums.txt") != 1 {
		t.Fatalf("checksums downloads = %d, want 1", release.Count("checksums.txt"))
	}
	if release.Count(archiveName) != 0 {
		t.Fatalf("archive downloads = %d, want 0", release.Count(archiveName))
	}
}

func TestUpdateRejectsChecksumMismatch(t *testing.T) {
	opts := testOptions(t)
	archiveName := testArchiveName(t)
	archiveData := testArchive(t, []byte("new binary"))
	release := newInstallFakeRelease(t, map[string][]byte{
		archiveName:     archiveData,
		"checksums.txt": []byte(strings.Repeat("0", 64) + "  " + archiveName + "\n"),
	})
	opts.ReleaseURL = release.URL()
	paths := createOwnedInstall(t, opts, []byte("old binary"), strings.Repeat("a", 64))
	opts.Executable = paths.InstallPath

	_, err := Update(context.Background(), opts)
	if err == nil {
		t.Fatal("Update() error = nil, want checksum error")
	}
	if IsUsageError(err) {
		t.Fatalf("Update() error = usage error, want update failure: %v", err)
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Update() error missing checksum mismatch: %v", err)
	}
}

func TestUpdateReplacesOwnedInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("active Windows executable replacement is staged after process exit")
	}
	opts := testOptions(t)
	archiveName := testArchiveName(t)
	nextBinary := []byte("new binary")
	archiveData := testArchive(t, nextBinary)
	archiveSHA := checksumHex(archiveData)
	release := newInstallFakeRelease(t, map[string][]byte{
		archiveName:     archiveData,
		"checksums.txt": []byte(archiveSHA + "  " + archiveName + "\n"),
	})
	opts.ReleaseURL = release.URL()
	paths := createOwnedInstall(t, opts, []byte("old binary"), strings.Repeat("a", 64))
	opts.Executable = paths.InstallPath

	result, err := Update(context.Background(), opts)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if result.AlreadyCurrent || result.Staged {
		t.Fatalf("result = %+v, want direct replacement", result)
	}
	binary, err := os.ReadFile(paths.InstallPath)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if !bytes.Equal(binary, nextBinary) {
		t.Fatalf("installed binary = %q, want %q", binary, nextBinary)
	}
	metadata := readMetadataForTest(t, paths.MetadataPath)
	if metadata.ArchiveSHA256 != archiveSHA || metadata.BinarySHA256 != checksumHex(nextBinary) {
		t.Fatalf("metadata = %+v, want archive and binary hashes", metadata)
	}
}

func testOptions(t *testing.T) Options {
	t.Helper()
	tempDir := t.TempDir()
	opts := Options{
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		HomeDir:      filepath.Join(tempDir, "home"),
		LocalAppData: filepath.Join(tempDir, "LocalAppData"),
		XDGStateHome: filepath.Join(tempDir, "state"),
		Now: func() time.Time {
			return time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
		},
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skipf("unsupported test OS: %s", runtime.GOOS)
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("unsupported test architecture: %s", runtime.GOARCH)
	}
	return opts
}

func createOwnedInstall(t *testing.T, opts Options, binary []byte, archiveSHA string) Paths {
	t.Helper()
	paths, err := ResolvePaths(opts)
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if err := os.MkdirAll(paths.InstallDir, 0o755); err != nil {
		t.Fatalf("create install dir: %v", err)
	}
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		t.Fatalf("create state dir: %v", err)
	}
	if err := os.WriteFile(paths.InstallPath, binary, 0o755); err != nil {
		t.Fatalf("write install: %v", err)
	}
	metadata := Metadata{
		SchemaVersion: 1,
		Product:       productName,
		Channel:       DefaultChannel,
		InstallPath:   paths.InstallPath,
		ArchiveName:   testArchiveName(t),
		ArchiveSHA256: archiveSHA,
		BinarySHA256:  checksumHex(binary),
		InstalledAt:   "2026-06-04T12:00:00Z",
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	if err := os.WriteFile(paths.MetadataPath, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	return paths
}

func readMetadataForTest(t *testing.T, path string) Metadata {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatalf("parse metadata: %v", err)
	}
	return metadata
}

func testArchiveName(t *testing.T) string {
	t.Helper()
	name, err := archiveName(DefaultPackagePrefix, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	return name
}

func testArchive(t *testing.T, binary []byte) []byte {
	t.Helper()
	if runtime.GOOS == "windows" {
		return testZip(t, binary)
	}
	return testTarGz(t, binary)
}

func testTarGz(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gzipWriter)
	header := &tar.Header{
		Name: productName,
		Mode: 0o755,
		Size: int64(len(binary)),
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tarWriter.Write(binary); err != nil {
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

func testZip(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zipWriter := zip.NewWriter(&buf)
	writer, err := zipWriter.Create(productName + ".exe")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := writer.Write(binary); err != nil {
		t.Fatalf("write zip body: %v", err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func checksumHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type installFakeRelease struct {
	server    *httptest.Server
	closeOnce sync.Once
	mu        sync.Mutex
	assets    map[string][]byte
	counts    map[string]int
}

func newInstallFakeRelease(t *testing.T, assets map[string][]byte) *installFakeRelease {
	t.Helper()
	release := &installFakeRelease{
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

func (r *installFakeRelease) URL() string {
	return r.server.URL
}

func (r *installFakeRelease) Count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[name]
}

func (r *installFakeRelease) Close() {
	r.closeOnce.Do(r.server.Close)
}

func TestUsageErrorDetection(t *testing.T) {
	err := UsageError{Err: errors.New("usage")}
	if !IsUsageError(err) {
		t.Fatal("IsUsageError() = false, want true")
	}
}
