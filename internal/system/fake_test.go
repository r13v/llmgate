package system

import (
	"context"
	"io/fs"
	"sort"
	"strings"
	"time"
)

type FakeFileSystem struct {
	entries map[string]fakeFileEntry
}

type fakeFileEntry struct {
	data []byte
	mode fs.FileMode
	dir  bool
}

func NewFakeFileSystem() *FakeFileSystem {
	return &FakeFileSystem{entries: make(map[string]fakeFileEntry)}
}

func (f *FakeFileSystem) AddFile(name string, data []byte) {
	copied := append([]byte(nil), data...)
	f.entries[name] = fakeFileEntry{data: copied, mode: 0o600}
}

func (f *FakeFileSystem) AddDir(name string) {
	f.entries[name] = fakeFileEntry{mode: 0o700 | fs.ModeDir, dir: true}
}

func (f *FakeFileSystem) ReadFile(name string) ([]byte, error) {
	entry, ok := f.entries[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), entry.data...), nil
}

func (f *FakeFileSystem) WriteFile(name string, data []byte, perm fs.FileMode) error {
	copied := append([]byte(nil), data...)
	f.entries[name] = fakeFileEntry{data: copied, mode: perm}
	return nil
}

func (f *FakeFileSystem) WriteFileExclusive(name string, data []byte, perm fs.FileMode) error {
	if _, ok := f.entries[name]; ok {
		return fs.ErrExist
	}
	return f.WriteFile(name, data, perm)
}

func (f *FakeFileSystem) MkdirAll(name string, perm fs.FileMode) error {
	f.entries[name] = fakeFileEntry{mode: perm | fs.ModeDir, dir: true}
	return nil
}

func (f *FakeFileSystem) Stat(name string) (fs.FileInfo, error) {
	entry, ok := f.entries[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return fakeFileInfo{name: pathBase(name), entry: entry}, nil
}

func (f *FakeFileSystem) Rename(oldPath, newPath string) error {
	entry, ok := f.entries[oldPath]
	if !ok {
		return fs.ErrNotExist
	}
	delete(f.entries, oldPath)
	f.entries[newPath] = entry
	return nil
}

func (f *FakeFileSystem) Remove(name string) error {
	if _, ok := f.entries[name]; !ok {
		return fs.ErrNotExist
	}
	delete(f.entries, name)
	return nil
}

func (f *FakeFileSystem) Chmod(name string, mode fs.FileMode) error {
	entry, ok := f.entries[name]
	if !ok {
		return fs.ErrNotExist
	}
	entry.mode = mode
	f.entries[name] = entry
	return nil
}

type fakeFileInfo struct {
	name  string
	entry fakeFileEntry
}

func (f fakeFileInfo) Name() string {
	return f.name
}

func (f fakeFileInfo) Size() int64 {
	return int64(len(f.entry.data))
}

func (f fakeFileInfo) Mode() fs.FileMode {
	return f.entry.mode
}

func (f fakeFileInfo) ModTime() time.Time {
	return time.Time{}
}

func (f fakeFileInfo) IsDir() bool {
	return f.entry.dir
}

func (f fakeFileInfo) Sys() any {
	return nil
}

type FakeEnvironment struct {
	Values map[string]string
}

func (e FakeEnvironment) Environ() []string {
	values := make([]string, 0, len(e.Values))
	for name, value := range e.Values {
		values = append(values, name+"="+value)
	}
	sort.Strings(values)
	return values
}

func (e FakeEnvironment) LookupEnv(name string) (string, bool) {
	value, ok := e.Values[name]
	return value, ok
}

func (e FakeEnvironment) Getenv(name string) string {
	return e.Values[name]
}

func (e FakeEnvironment) Setenv(name, value string) error {
	if e.Values == nil {
		e.Values = make(map[string]string)
	}
	e.Values[name] = value
	return nil
}

func (e FakeEnvironment) Unsetenv(name string) error {
	delete(e.Values, name)
	return nil
}

type FakePlatform struct {
	TargetOS string
	Home     string
	WorkDir  string
	HomeErr  error
	WorkErr  error
}

func (p FakePlatform) GOOS() string {
	return p.TargetOS
}

func (p FakePlatform) HomeDir() (string, error) {
	return p.Home, p.HomeErr
}

func (p FakePlatform) WorkingDir() (string, error) {
	return p.WorkDir, p.WorkErr
}

type FakeTerminal struct {
	Interactive bool
}

func (t FakeTerminal) IsInteractive() bool {
	return t.Interactive
}

type FakeCommandCall struct {
	Name string
	Args []string
}

type FakeCommandRunner struct {
	Result CommandResult
	Err    error
	Calls  []FakeCommandCall
}

func (r *FakeCommandRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	r.Calls = append(r.Calls, FakeCommandCall{Name: name, Args: append([]string(nil), args...)})
	return r.Result, r.Err
}

type FakeWindowsUserEnvironment struct {
	Values map[string]string
	Err    error
}

func (e FakeWindowsUserEnvironment) Lookup(name string) (string, bool, error) {
	if e.Err != nil {
		return "", false, e.Err
	}
	value, ok := e.Values[name]
	return value, ok, nil
}

func (e FakeWindowsUserEnvironment) Snapshot(names []string) (map[string]string, error) {
	if e.Err != nil {
		return nil, e.Err
	}
	values := make(map[string]string, len(names))
	for _, name := range names {
		if value, ok := e.Values[name]; ok {
			values[name] = value
		}
	}
	return values, nil
}

func (e FakeWindowsUserEnvironment) Set(name, value string) error {
	if e.Err != nil {
		return e.Err
	}
	if e.Values == nil {
		e.Values = make(map[string]string)
	}
	e.Values[name] = value
	return nil
}

func (e FakeWindowsUserEnvironment) Delete(name string) error {
	if e.Err != nil {
		return e.Err
	}
	delete(e.Values, name)
	return nil
}

func pathBase(name string) string {
	name = strings.TrimRight(name, `/\`)
	if name == "" {
		return ""
	}
	index := strings.LastIndexAny(name, `/\`)
	if index < 0 {
		return name
	}
	return name[index+1:]
}
