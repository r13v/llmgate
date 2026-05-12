//go:build e2e

package e2e

import (
	"io/fs"
	"sort"
	"testing"
)

func withPlatform(targetOS, home, work string) harnessOption {
	return func(h *harness) {
		h.platform = trackingPlatform{targetOS: targetOS, home: home, work: work}
		h.fs.addDir(home)
		h.fs.addDir(work)
	}
}

func withShell(shell string) harnessOption {
	return func(h *harness) {
		h.env.values["SHELL"] = shell
	}
}

func (h *harness) resetCounts() {
	h.fs.resetCounts()
	h.env.resetCounts()
	h.commands.resetCounts()
	h.winEnv.resetCounts()
	h.gateway.resetCounts()
}

func (f *trackingFS) addFile(path string, data []byte, mode fs.FileMode) {
	f.files[path] = append([]byte(nil), data...)
	f.modes[path] = mode
	f.addDir(parentDir(path))
}

func (f *trackingFS) resetCounts() {
	f.readOps = 0
	f.statOps = 0
	f.writeOps = 0
	f.mkdirOps = 0
	f.renameOps = 0
	f.removeOps = 0
	f.chmodOps = 0
}

func (e *trackingEnv) resetCounts() {
	e.environOps = 0
	e.lookupOps = 0
	e.getenvOps = 0
	e.mutationOps = 0
}

func (r *trackingCommandRunner) resetCounts() {
	r.calls = 0
}

func (e *trackingWindowsEnv) resetCounts() {
	e.lookupOps = 0
	e.snapshotOps = 0
	e.setOps = 0
	e.deleteOps = 0
}

func (g *fakeGateway) resetCounts() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.listCalls = 0
	g.fallbackCalls = 0
	g.probeCalls = 0
	g.paths = nil
	g.probedModels = nil
	g.probePingBodies = 0
}

func (p *scriptedPrompter) sawSelectOption(label string) bool {
	for _, record := range p.records {
		if record.kind != "select" {
			continue
		}
		for _, option := range record.options {
			if option == label {
				return true
			}
		}
	}
	return false
}

func (p *scriptedPrompter) sawPromptTitle(title string) bool {
	for _, record := range p.records {
		if record.title == title {
			return true
		}
	}
	return false
}

func assertFileNotContains(t *testing.T, f *trackingFS, path, notWant string) {
	t.Helper()
	data, ok := f.file(path)
	if !ok {
		t.Fatalf("file %s missing", path)
	}
	assertNotContains(t, data, notWant)
}

func sortedCopy(values []string) []string {
	copied := append([]string(nil), values...)
	sort.Strings(copied)
	return copied
}
