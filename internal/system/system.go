package system

import (
	"errors"
	"io/fs"
	"os"
	"runtime"

	"github.com/r13v/llmgate/internal/core"
)

var ErrUnsupportedWindowsUserEnvironment = errors.New("windows user environment is not supported on this platform")

type FileSystem interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	Stat(name string) (fs.FileInfo, error)
	Rename(oldPath, newPath string) error
	Remove(name string) error
	Chmod(name string, mode fs.FileMode) error
}

type RealFileSystem struct{}

func (RealFileSystem) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (RealFileSystem) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(name, data, perm)
}

func (RealFileSystem) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (RealFileSystem) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(name)
}

func (RealFileSystem) Rename(oldPath, newPath string) error {
	return os.Rename(oldPath, newPath)
}

func (RealFileSystem) Remove(name string) error {
	return os.Remove(name)
}

func (RealFileSystem) Chmod(name string, mode fs.FileMode) error {
	return os.Chmod(name, mode)
}

type ProcessEnvironment interface {
	LookupEnv(name string) (string, bool)
	Getenv(name string) string
}

type RealEnvironment struct{}

func (RealEnvironment) LookupEnv(name string) (string, bool) {
	return os.LookupEnv(name)
}

func (RealEnvironment) Getenv(name string) string {
	return os.Getenv(name)
}

type Platform interface {
	GOOS() string
	HomeDir() (string, error)
	WorkingDir() (string, error)
}

type RealPlatform struct{}

func (RealPlatform) GOOS() string {
	return runtime.GOOS
}

func (RealPlatform) HomeDir() (string, error) {
	return os.UserHomeDir()
}

func (RealPlatform) WorkingDir() (string, error) {
	return os.Getwd()
}

type WindowsUserEnvironment interface {
	Snapshot(names []string) (map[string]string, error)
	Set(name, value string) error
}

type unsupportedWindowsUserEnvironment struct{}

func (unsupportedWindowsUserEnvironment) Snapshot([]string) (map[string]string, error) {
	return nil, ErrUnsupportedWindowsUserEnvironment
}

func (unsupportedWindowsUserEnvironment) Set(string, string) error {
	return ErrUnsupportedWindowsUserEnvironment
}

type System struct {
	FS         FileSystem
	Env        ProcessEnvironment
	Commands   CommandRunner
	Terminal   Terminal
	Platform   Platform
	WindowsEnv WindowsUserEnvironment
}

func NewRealSystem(stdin, stdout *os.File) System {
	return System{
		FS:         RealFileSystem{},
		Env:        RealEnvironment{},
		Commands:   RealCommandRunner{},
		Terminal:   RealTerminal{Stdin: stdin, Stdout: stdout},
		Platform:   RealPlatform{},
		WindowsEnv: NewWindowsUserEnvironment(),
	}
}

func (s System) PathOptions() (PathOptions, error) {
	platform := s.Platform
	if platform == nil {
		platform = RealPlatform{}
	}

	home, err := platform.HomeDir()
	if err != nil {
		return PathOptions{}, err
	}
	workingDir, err := platform.WorkingDir()
	if err != nil {
		return PathOptions{}, err
	}

	var shell string
	var appData string
	if s.Env != nil {
		shell = s.Env.Getenv("SHELL")
		appData = s.Env.Getenv("APPDATA")
	}

	return PathOptions{
		GOOS:       platform.GOOS(),
		HomeDir:    home,
		WorkingDir: workingDir,
		Shell:      shell,
		AppData:    appData,
	}, nil
}

func (s System) DetectPaths() (DiscoveredPaths, error) {
	options, err := s.PathOptions()
	if err != nil {
		return DiscoveredPaths{}, err
	}

	fileSystem := s.FS
	if fileSystem == nil {
		fileSystem = RealFileSystem{}
	}
	return DetectPaths(fileSystem, options)
}

func ManagedEnvironment(env ProcessEnvironment) map[string]string {
	values := make(map[string]string)
	if env == nil {
		return values
	}
	for _, name := range core.AllManagedNames() {
		value, ok := env.LookupEnv(name)
		if ok {
			values[name] = value
		}
	}
	return values
}
