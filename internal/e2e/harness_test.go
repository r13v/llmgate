package e2e

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/r13v/llmgate/internal/apply"
	"github.com/r13v/llmgate/internal/system"
	"github.com/r13v/llmgate/internal/wizard"
)

const (
	testToken    = "sk-test-token-1234567890"
	altTestToken = "sk-test-token-0987654321"
	leakedToken  = "sk-leaked-token-1234567890"
	sonnetModel  = "claude-sonnet-4"
	haikuModel   = "claude-haiku-4"
	opusModel    = "claude-opus-4"
	missingModel = "claude-missing-4"
	staleModel   = "claude-stale-2"
)

var recommendedModels = []string{haikuModel, opusModel, sonnetModel}

type harness struct {
	fs       *trackingFS
	env      *trackingEnv
	commands *trackingCommandRunner
	terminal trackingTerminal
	platform trackingPlatform
	winEnv   *trackingWindowsEnv
	gateway  *fakeGateway
	output   bytes.Buffer
}

func newHarness(t *testing.T, opts ...harnessOption) *harness {
	t.Helper()

	h := &harness{
		fs: newTrackingFS(),
		env: &trackingEnv{values: map[string]string{
			"SHELL": "/bin/zsh",
		}},
		commands: &trackingCommandRunner{
			result: system.CommandResult{Stdout: "claude 1.0.0\n"},
		},
		terminal: trackingTerminal{interactive: true},
		platform: trackingPlatform{
			targetOS: "linux",
			home:     "/home/ada",
			work:     "/home/ada/project",
		},
		winEnv:  &trackingWindowsEnv{values: map[string]string{}},
		gateway: newFakeGateway(t, fakeGatewayOptions{models: recommendedModels}),
	}
	h.fs.addDir(h.platform.home)
	h.fs.addDir(h.platform.work)
	t.Cleanup(h.gateway.close)

	for _, opt := range opts {
		opt(h)
	}
	return h
}

type harnessOption func(*harness)

func withGatewayOptions(t *testing.T, opts fakeGatewayOptions) harnessOption {
	return func(h *harness) {
		h.gateway.close()
		h.gateway = newFakeGateway(t, opts)
	}
}

func (h *harness) runAccessible(input string, opts ...runOption) (string, error) {
	prompter := wizard.HuhPrompter{
		In:         newOneByteReader(input),
		Output:     &h.output,
		Accessible: true,
	}
	return h.runWithPrompter(prompter, opts...)
}

func (h *harness) runScripted(responses []promptResponse, opts ...runOption) (string, error) {
	prompter := &scriptedPrompter{responses: responses}
	output, err := h.runWithPrompter(prompter, opts...)
	return output, err
}

func (h *harness) runWithPrompter(prompter wizard.Prompter, opts ...runOption) (string, error) {
	config := runConfig{interactive: true}
	for _, opt := range opts {
		opt(&config)
	}

	h.output.Reset()
	h.terminal.interactive = config.interactive
	err := wizard.Run(context.Background(), wizard.Options{
		System:         h.system(),
		Gateway:        h.gateway.client(),
		Prompter:       prompter,
		Output:         &h.output,
		CommandTimeout: time.Second,
		ApplyOptions: apply.ApplyOptions{
			Now: func() time.Time {
				return time.Date(2026, 5, 13, 1, 2, 3, 0, time.UTC)
			},
		},
	})
	return h.output.String(), err
}

type runConfig struct {
	interactive bool
}

type runOption func(*runConfig)

func nonInteractive() runOption {
	return func(c *runConfig) {
		c.interactive = false
	}
}

func (h *harness) system() system.System {
	return system.System{
		FS:         h.fs,
		Env:        h.env,
		Commands:   h.commands,
		Terminal:   h.terminal,
		Platform:   h.platform,
		WindowsEnv: h.winEnv,
	}
}

type trackingFS struct {
	files     map[string][]byte
	modes     map[string]fs.FileMode
	dirs      map[string]bool
	readOps   int
	statOps   int
	writeOps  int
	mkdirOps  int
	renameOps int
	removeOps int
	chmodOps  int
	afterMove func(finalPath string)
}

func newTrackingFS() *trackingFS {
	return &trackingFS{
		files: make(map[string][]byte),
		modes: make(map[string]fs.FileMode),
		dirs:  make(map[string]bool),
	}
}

func (f *trackingFS) addDir(path string) {
	if path == "" || path == "." {
		return
	}
	if f.dirs[path] {
		return
	}
	f.dirs[path] = true
	parent := parentDir(path)
	if parent != path {
		f.addDir(parent)
	}
}

func (f *trackingFS) ReadFile(path string) ([]byte, error) {
	f.readOps++
	data, ok := f.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}

func (f *trackingFS) WriteFile(path string, data []byte, mode fs.FileMode) error {
	f.writeOps++
	f.files[path] = append([]byte(nil), data...)
	f.modes[path] = mode
	f.addDir(parentDir(path))
	return nil
}

func (f *trackingFS) MkdirAll(path string, _ fs.FileMode) error {
	f.mkdirOps++
	f.addDir(path)
	return nil
}

func (f *trackingFS) Stat(path string) (fs.FileInfo, error) {
	f.statOps++
	if f.dirs[path] {
		return fakeInfo{name: path, mode: fs.ModeDir | 0o700, dir: true}, nil
	}
	data, ok := f.files[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return fakeInfo{name: path, size: int64(len(data)), mode: f.modes[path]}, nil
}

func (f *trackingFS) Rename(oldPath, newPath string) error {
	f.writeOps++
	f.renameOps++
	data, ok := f.files[oldPath]
	if !ok {
		return fs.ErrNotExist
	}
	f.files[newPath] = append([]byte(nil), data...)
	f.modes[newPath] = f.modes[oldPath]
	delete(f.files, oldPath)
	delete(f.modes, oldPath)
	if f.afterMove != nil {
		f.afterMove(newPath)
	}
	return nil
}

func (f *trackingFS) Remove(path string) error {
	f.writeOps++
	f.removeOps++
	delete(f.files, path)
	delete(f.modes, path)
	return nil
}

func (f *trackingFS) Chmod(path string, mode fs.FileMode) error {
	f.chmodOps++
	if _, ok := f.files[path]; ok {
		f.modes[path] = mode
		return nil
	}
	if f.dirs[path] {
		return nil
	}
	return fs.ErrNotExist
}

func (f *trackingFS) mutationOps() int {
	return f.writeOps + f.mkdirOps + f.renameOps + f.removeOps + f.chmodOps
}

func (f *trackingFS) file(path string) (string, bool) {
	data, ok := f.files[path]
	return string(data), ok
}

type fakeInfo struct {
	name string
	size int64
	mode fs.FileMode
	dir  bool
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return i.size }
func (i fakeInfo) Mode() fs.FileMode  { return i.mode }
func (i fakeInfo) ModTime() time.Time { return time.Time{} }
func (i fakeInfo) IsDir() bool        { return i.dir }
func (i fakeInfo) Sys() any           { return nil }

type trackingEnv struct {
	values      map[string]string
	environOps  int
	lookupOps   int
	getenvOps   int
	mutationOps int
}

func (e *trackingEnv) Environ() []string {
	e.environOps++
	values := make([]string, 0, len(e.values))
	for name, value := range e.values {
		values = append(values, name+"="+value)
	}
	sort.Strings(values)
	return values
}

func (e *trackingEnv) LookupEnv(name string) (string, bool) {
	e.lookupOps++
	value, ok := e.values[name]
	return value, ok
}

func (e *trackingEnv) Getenv(name string) string {
	e.getenvOps++
	return e.values[name]
}

func (e *trackingEnv) Setenv(name, value string) error {
	e.mutationOps++
	e.values[name] = value
	return nil
}

func (e *trackingEnv) Unsetenv(name string) error {
	e.mutationOps++
	delete(e.values, name)
	return nil
}

func (e *trackingEnv) readOps() int {
	return e.environOps + e.lookupOps + e.getenvOps
}

type trackingCommandRunner struct {
	result system.CommandResult
	err    error
	calls  int
}

func (r *trackingCommandRunner) Run(context.Context, string, ...string) (system.CommandResult, error) {
	r.calls++
	return r.result, r.err
}

type trackingTerminal struct {
	interactive bool
}

func (t trackingTerminal) IsInteractive() bool {
	return t.interactive
}

type trackingPlatform struct {
	targetOS string
	home     string
	work     string
}

func (p trackingPlatform) GOOS() string {
	return p.targetOS
}

func (p trackingPlatform) HomeDir() (string, error) {
	return p.home, nil
}

func (p trackingPlatform) WorkingDir() (string, error) {
	return p.work, nil
}

type trackingWindowsEnv struct {
	values      map[string]string
	lookupOps   int
	snapshotOps int
	setOps      int
	deleteOps   int
}

func (e *trackingWindowsEnv) Lookup(name string) (string, bool, error) {
	e.lookupOps++
	value, ok := e.values[name]
	return value, ok, nil
}

func (e *trackingWindowsEnv) Snapshot(names []string) (map[string]string, error) {
	e.snapshotOps++
	values := make(map[string]string)
	for _, name := range names {
		if value, ok := e.values[name]; ok {
			values[name] = value
		}
	}
	return values, nil
}

func (e *trackingWindowsEnv) Set(name, value string) error {
	e.setOps++
	e.values[name] = value
	return nil
}

func (e *trackingWindowsEnv) Delete(name string) error {
	e.deleteOps++
	delete(e.values, name)
	return nil
}

func (e *trackingWindowsEnv) readOps() int {
	return e.lookupOps + e.snapshotOps
}

func (e *trackingWindowsEnv) mutationOps() int {
	return e.setOps + e.deleteOps
}

type promptResponse struct {
	kind    string
	confirm bool
	value   string
	values  []string
	err     error
}

type promptRecord struct {
	kind        string
	title       string
	description string
	options     []string
}

type scriptedPrompter struct {
	responses []promptResponse
	records   []promptRecord
	index     int
}

func (p *scriptedPrompter) Confirm(_ context.Context, prompt wizard.ConfirmPrompt) (bool, error) {
	p.records = append(p.records, promptRecord{kind: "confirm", title: prompt.Title, description: prompt.Description})
	response := p.next("confirm")
	if response.err != nil {
		return false, response.err
	}
	return response.confirm, nil
}

func (p *scriptedPrompter) Input(_ context.Context, prompt wizard.InputPrompt) (string, error) {
	p.records = append(p.records, promptRecord{kind: "input", title: prompt.Title, description: prompt.Description})
	response := p.next("input")
	return response.value, response.err
}

func (p *scriptedPrompter) Select(_ context.Context, prompt wizard.SelectPrompt) (string, error) {
	options := make([]string, 0, len(prompt.Options))
	for _, option := range prompt.Options {
		options = append(options, option.Label)
	}
	p.records = append(p.records, promptRecord{kind: "select", title: prompt.Title, description: prompt.Description, options: options})
	response := p.next("select")
	if response.err != nil {
		return "", response.err
	}
	if response.value == "" {
		return prompt.Default, nil
	}
	return response.value, nil
}

func (p *scriptedPrompter) MultiSelect(_ context.Context, prompt wizard.MultiSelectPrompt) ([]string, error) {
	options := make([]string, 0, len(prompt.Options))
	defaults := make([]string, 0, len(prompt.Options))
	for _, option := range prompt.Options {
		options = append(options, option.Label)
		if option.Selected {
			defaults = append(defaults, option.Value)
		}
	}
	p.records = append(p.records, promptRecord{kind: "multiselect", title: prompt.Title, description: prompt.Description, options: options})
	response := p.next("multiselect")
	if response.err != nil {
		return nil, response.err
	}
	if response.values == nil {
		return defaults, nil
	}
	return response.values, nil
}

func (p *scriptedPrompter) next(kind string) promptResponse {
	if p.index >= len(p.responses) {
		return promptResponse{kind: kind, err: errors.New("unexpected prompt " + kind)}
	}
	response := p.responses[p.index]
	p.index++
	if response.kind != kind {
		return promptResponse{kind: kind, err: errors.New("unexpected prompt " + kind + ", want " + response.kind)}
	}
	return response
}

type oneByteReader struct {
	reader *strings.Reader
}

func newOneByteReader(input string) *oneByteReader {
	return &oneByteReader{reader: strings.NewReader(input)}
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.reader.Read(p)
}

func parentDir(path string) string {
	index := strings.LastIndexAny(path, `/\`)
	if index <= 0 {
		return "."
	}
	return path[:index]
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output missing %q:\n%s", want, got)
	}
}

func assertNotContains(t *testing.T, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Fatalf("output unexpectedly contained %q:\n%s", want, got)
	}
}

func assertFileContains(t *testing.T, f *trackingFS, path, want string) {
	t.Helper()
	data, ok := f.file(path)
	if !ok {
		t.Fatalf("file %s missing", path)
	}
	assertContains(t, data, want)
}

func assertNoSecretLeak(t *testing.T, output string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if strings.Contains(output, secret) {
			t.Fatalf("output leaked secret %q:\n%s", secret, output)
		}
	}
}
