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
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultReleaseURL    = "https://github.com/r13v/llmgate/releases/download/main"
	DefaultPackagePrefix = "llmgate-main"
	DefaultChannel       = "main"

	productName = "llmgate"
)

type Options struct {
	ReleaseURL    string
	PackagePrefix string
	Channel       string
	GOOS          string
	GOARCH        string
	HomeDir       string
	LocalAppData  string
	XDGStateHome  string
	Executable    string
	HTTPClient    *http.Client
	Now           func() time.Time
}

type Paths struct {
	GOOS         string
	InstallDir   string
	InstallPath  string
	StateDir     string
	MetadataPath string
}

type Metadata struct {
	SchemaVersion int    `json:"schema_version"`
	Product       string `json:"product"`
	Channel       string `json:"channel"`
	InstallPath   string `json:"install_path"`
	ArchiveName   string `json:"archive_name"`
	ArchiveSHA256 string `json:"archive_sha256"`
	BinarySHA256  string `json:"binary_sha256"`
	InstalledAt   string `json:"installed_at"`
}

type Result struct {
	Paths          Paths
	Metadata       Metadata
	AlreadyCurrent bool
	Staged         bool
}

type UsageError struct {
	Err error
}

func (e UsageError) Error() string {
	return e.Err.Error()
}

func (e UsageError) Unwrap() error {
	return e.Err
}

func IsUsageError(err error) bool {
	var usageErr UsageError
	return errors.As(err, &usageErr)
}

func Update(ctx context.Context, opts Options) (Result, error) {
	opts = withDefaults(opts)

	paths, err := ResolvePaths(opts)
	if err != nil {
		return Result{}, UsageError{Err: err}
	}
	executable, err := executablePath(opts.Executable)
	if err != nil {
		return Result{}, UsageError{Err: err}
	}
	if !samePath(opts.GOOS, executable, paths.InstallPath) {
		return Result{}, UsageError{Err: fmt.Errorf("llmgate update must be run from %s; run the one-line install command first", paths.InstallPath)}
	}

	metadata, err := readOwnedMetadata(paths, opts.Channel)
	if err != nil {
		return Result{}, UsageError{Err: err}
	}

	archiveName, err := archiveName(opts.PackagePrefix, opts.GOOS, opts.GOARCH)
	if err != nil {
		return Result{}, UsageError{Err: err}
	}
	expectedArchiveSHA, err := fetchExpectedArchiveSHA(ctx, opts, archiveName)
	if err != nil {
		return Result{}, err
	}

	result := Result{Paths: paths}
	if metadata.ArchiveSHA256 == expectedArchiveSHA {
		result.Metadata = metadata
		result.AlreadyCurrent = true
		return result, nil
	}

	archivePath, cleanup, err := downloadArchive(ctx, opts, archiveName, expectedArchiveSHA)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()

	binary, err := extractBinary(archivePath, opts.GOOS)
	if err != nil {
		return Result{}, err
	}
	binarySHA := sha256Hex(binary)
	now := opts.Now()
	nextMetadata := Metadata{
		SchemaVersion: 1,
		Product:       productName,
		Channel:       opts.Channel,
		InstallPath:   paths.InstallPath,
		ArchiveName:   archiveName,
		ArchiveSHA256: expectedArchiveSHA,
		BinarySHA256:  binarySHA,
		InstalledAt:   now.UTC().Format(time.RFC3339),
	}

	staged, err := replaceOwnedInstall(paths, opts.Channel, binary, nextMetadata)
	if err != nil {
		return Result{}, err
	}
	result.Metadata = nextMetadata
	result.Staged = staged
	return result, nil
}

func ResolvePaths(opts Options) (Paths, error) {
	opts = withDefaults(opts)
	if opts.HomeDir == "" {
		return Paths{}, fmt.Errorf("home directory is required")
	}

	switch opts.GOOS {
	case "darwin", "linux":
		stateBase := opts.XDGStateHome
		if stateBase == "" {
			stateBase = joinForOS(opts.GOOS, opts.HomeDir, ".local", "state")
		}
		installDir := joinForOS(opts.GOOS, opts.HomeDir, ".local", "bin")
		stateDir := joinForOS(opts.GOOS, stateBase, productName)
		return Paths{
			GOOS:         opts.GOOS,
			InstallDir:   installDir,
			InstallPath:  joinForOS(opts.GOOS, installDir, productName),
			StateDir:     stateDir,
			MetadataPath: joinForOS(opts.GOOS, stateDir, "install.json"),
		}, nil
	case "windows":
		localAppData := opts.LocalAppData
		if localAppData == "" {
			localAppData = joinForOS(opts.GOOS, opts.HomeDir, "AppData", "Local")
		}
		installDir := joinForOS(opts.GOOS, localAppData, "Programs", productName)
		stateDir := joinForOS(opts.GOOS, localAppData, productName)
		return Paths{
			GOOS:         opts.GOOS,
			InstallDir:   installDir,
			InstallPath:  joinForOS(opts.GOOS, installDir, productName+".exe"),
			StateDir:     stateDir,
			MetadataPath: joinForOS(opts.GOOS, stateDir, "install.json"),
		}, nil
	default:
		return Paths{}, fmt.Errorf("unsupported OS: %s", opts.GOOS)
	}
}

func withDefaults(opts Options) Options {
	if opts.ReleaseURL == "" {
		opts.ReleaseURL = DefaultReleaseURL
	}
	if opts.PackagePrefix == "" {
		opts.PackagePrefix = DefaultPackagePrefix
	}
	if opts.Channel == "" {
		opts.Channel = DefaultChannel
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return opts
}

func executablePath(path string) (string, error) {
	if path == "" {
		var err error
		path, err = os.Executable()
		if err != nil {
			return "", fmt.Errorf("locate current executable: %w", err)
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func readOwnedMetadata(paths Paths, channel string) (Metadata, error) {
	info, err := os.Lstat(paths.InstallPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Metadata{}, fmt.Errorf("canonical installed command is missing: %s", paths.InstallPath)
		}
		return Metadata{}, fmt.Errorf("inspect canonical installed command: %w", err)
	}
	if info.IsDir() {
		return Metadata{}, fmt.Errorf("canonical installed command is a directory: %s", paths.InstallPath)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return Metadata{}, fmt.Errorf("canonical installed command is a symlink and will not be overwritten: %s", paths.InstallPath)
	}

	data, err := os.ReadFile(paths.MetadataPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Metadata{}, fmt.Errorf("install metadata is missing: %s", paths.MetadataPath)
		}
		return Metadata{}, fmt.Errorf("read install metadata: %w", err)
	}
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return Metadata{}, fmt.Errorf("parse install metadata: %w", err)
	}
	if metadata.Product != productName {
		return Metadata{}, fmt.Errorf("install metadata product is %q, want %q", metadata.Product, productName)
	}
	if metadata.Channel != channel {
		return Metadata{}, fmt.Errorf("install metadata channel is %q, want %q", metadata.Channel, channel)
	}
	if !samePath(paths.GOOS, metadata.InstallPath, paths.InstallPath) {
		return Metadata{}, fmt.Errorf("install metadata points at %s, want %s", metadata.InstallPath, paths.InstallPath)
	}
	if !isSHA256Hex(metadata.BinarySHA256) {
		return Metadata{}, fmt.Errorf("install metadata has invalid binary SHA-256")
	}
	binary, err := os.ReadFile(paths.InstallPath)
	if err != nil {
		return Metadata{}, fmt.Errorf("read canonical installed command: %w", err)
	}
	if actual := sha256Hex(binary); actual != strings.ToLower(metadata.BinarySHA256) {
		return Metadata{}, fmt.Errorf("canonical installed command changed outside llmgate; remove %s and rerun the one-line install command if you want llmgate to own it again", paths.InstallPath)
	}
	return metadata, nil
}

func fetchExpectedArchiveSHA(ctx context.Context, opts Options, name string) (string, error) {
	checksumsURL := strings.TrimRight(opts.ReleaseURL, "/") + "/checksums.txt"
	data, err := fetchBytes(ctx, opts.HTTPClient, checksumsURL)
	if err != nil {
		return "", fmt.Errorf("check latest llmgate release: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == name {
			sha := strings.ToLower(fields[0])
			if !isSHA256Hex(sha) {
				return "", fmt.Errorf("checksum entry for %s is not a SHA-256 digest", name)
			}
			return sha, nil
		}
	}
	return "", fmt.Errorf("checksum entry not found for %s", name)
}

func downloadArchive(ctx context.Context, opts Options, archiveName, expectedSHA string) (string, func(), error) {
	tempDir, err := os.MkdirTemp("", "llmgate-update-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temporary directory: %w", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}
	archivePath := filepath.Join(tempDir, archiveName)
	archiveURL := strings.TrimRight(opts.ReleaseURL, "/") + "/" + archiveName
	data, err := fetchBytes(ctx, opts.HTTPClient, archiveURL)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("download %s: %w", archiveName, err)
	}
	if actual := sha256Hex(data); actual != expectedSHA {
		cleanup()
		return "", nil, fmt.Errorf("checksum mismatch for %s", archiveName)
	}
	if err := os.WriteFile(archivePath, data, 0o600); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write temporary archive: %w", err)
	}
	return archivePath, cleanup, nil
}

func fetchBytes(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func extractBinary(archivePath, targetOS string) ([]byte, error) {
	if targetOS == "windows" {
		return extractZipBinary(archivePath)
	}
	return extractTarGzBinary(archivePath)
}

func extractTarGzBinary(archivePath string) ([]byte, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("open gzip archive: %w", err)
	}
	defer func() {
		_ = gzipReader.Close()
	}()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar archive: %w", err)
		}
		if header.Name != productName {
			continue
		}
		if header.FileInfo().IsDir() {
			return nil, fmt.Errorf("archive entry %s is a directory", productName)
		}
		return io.ReadAll(tarReader)
	}
	return nil, fmt.Errorf("archive did not contain %s", productName)
}

func extractZipBinary(archivePath string) ([]byte, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open zip archive: %w", err)
	}
	defer func() {
		_ = reader.Close()
	}()
	for _, file := range reader.File {
		if file.Name != productName+".exe" {
			continue
		}
		if file.FileInfo().IsDir() {
			return nil, fmt.Errorf("archive entry %s.exe is a directory", productName)
		}
		entry, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(entry)
		closeErr := entry.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return data, nil
	}
	return nil, fmt.Errorf("archive did not contain %s.exe", productName)
}

func replaceOwnedInstall(paths Paths, channel string, binary []byte, metadata Metadata) (bool, error) {
	if _, err := readOwnedMetadata(paths, channel); err != nil {
		return false, err
	}
	if err := os.MkdirAll(paths.InstallDir, 0o755); err != nil {
		return false, fmt.Errorf("create install directory: %w", err)
	}
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		return false, fmt.Errorf("create state directory: %w", err)
	}
	metadataData, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return false, err
	}
	metadataData = append(metadataData, '\n')

	if paths.GOOS == "windows" && runtime.GOOS == "windows" {
		return stageWindowsReplace(paths, binary, metadataData)
	}
	if err := writeAtomic(paths.InstallPath, binary, 0o755); err != nil {
		return false, err
	}
	if err := writeAtomic(paths.MetadataPath, metadataData, 0o600); err != nil {
		return false, err
	}
	return false, nil
}

func stageWindowsReplace(paths Paths, binary, metadata []byte) (bool, error) {
	newBinary, err := writeTemp(paths.InstallDir, productName+"-*.exe", binary, 0o755)
	if err != nil {
		return false, err
	}
	newMetadata, err := writeTemp(paths.StateDir, "install-*.json", metadata, 0o600)
	if err != nil {
		_ = os.Remove(newBinary)
		return false, err
	}
	script := []byte(`param(
	[int]$PidToWait,
	[string]$NewBinary,
	[string]$InstallPath,
	[string]$NewMetadata,
	[string]$MetadataPath,
	[string]$ScriptPath
)
$ErrorActionPreference = "Stop"
while (Get-Process -Id $PidToWait -ErrorAction SilentlyContinue) {
	Start-Sleep -Milliseconds 100
}
Move-Item -LiteralPath $NewBinary -Destination $InstallPath -Force
Move-Item -LiteralPath $NewMetadata -Destination $MetadataPath -Force
Remove-Item -LiteralPath $ScriptPath -Force -ErrorAction SilentlyContinue
`)
	scriptPath, err := writeTemp(paths.StateDir, "replace-*.ps1", script, 0o600)
	if err != nil {
		_ = os.Remove(newBinary)
		_ = os.Remove(newMetadata)
		return false, err
	}
	cmd := exec.Command(
		"powershell",
		"-NoProfile",
		"-ExecutionPolicy",
		"Bypass",
		"-File",
		scriptPath,
		strconv.Itoa(os.Getpid()),
		newBinary,
		paths.InstallPath,
		newMetadata,
		paths.MetadataPath,
		scriptPath,
	)
	if err := cmd.Start(); err != nil {
		_ = os.Remove(newBinary)
		_ = os.Remove(newMetadata)
		_ = os.Remove(scriptPath)
		return false, fmt.Errorf("start Windows replacement helper: %w", err)
	}
	return true, nil
}

func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	tempPath, err := writeTemp(dir, "."+filepath.Base(path)+".*.tmp", data, mode)
	if err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func writeTemp(dir, pattern string, data []byte, mode fs.FileMode) (string, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	path := file.Name()
	if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Chmod(path, mode); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func archiveName(prefix, targetOS, arch string) (string, error) {
	switch targetOS {
	case "darwin", "linux":
		return fmt.Sprintf("%s-%s-%s.tar.gz", prefix, targetOS, arch), nil
	case "windows":
		return fmt.Sprintf("%s-windows-%s.zip", prefix, arch), nil
	default:
		return "", fmt.Errorf("unsupported OS: %s", targetOS)
	}
}

func samePath(targetOS, a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if targetOS == "windows" {
		a = strings.ToLower(strings.ReplaceAll(a, "/", `\`))
		b = strings.ToLower(strings.ReplaceAll(b, "/", `\`))
	}
	return a == b
}

func joinForOS(targetOS string, elements ...string) string {
	separator := "/"
	if targetOS == "windows" {
		separator = `\`
	}
	var cleaned []string
	for index, element := range elements {
		if element == "" {
			continue
		}
		if index == 0 {
			cleaned = append(cleaned, strings.TrimRight(element, `/\`))
		} else {
			cleaned = append(cleaned, strings.Trim(element, `/\`))
		}
	}
	return strings.Join(cleaned, separator)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}
