package version

import (
	"fmt"
	"runtime"
	"strings"
)

var (
	version = "dev"
	commit  = ""
	date    = ""
)

type Info struct {
	Version string
	Commit  string
	Date    string
	Go      string
	OS      string
	Arch    string
}

func Current() Info {
	return Info{
		Version: fallback(version, "dev"),
		Commit:  commit,
		Date:    date,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
	}
}

func (i Info) String() string {
	var b strings.Builder

	fmt.Fprintf(&b, "llmgate %s\n", fallback(i.Version, "dev"))
	if i.Commit != "" {
		fmt.Fprintf(&b, "commit: %s\n", i.Commit)
	}
	if i.Date != "" {
		fmt.Fprintf(&b, "date: %s\n", i.Date)
	}
	fmt.Fprintf(&b, "go: %s\n", fallback(i.Go, runtime.Version()))
	fmt.Fprintf(&b, "platform: %s/%s\n", fallback(i.OS, runtime.GOOS), fallback(i.Arch, runtime.GOARCH))

	return b.String()
}

func fallback(value, replacement string) string {
	if value == "" {
		return replacement
	}
	return value
}
