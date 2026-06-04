package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/r13v/llmgate/internal/install"
	"github.com/r13v/llmgate/internal/redact"
	"github.com/r13v/llmgate/internal/system"
	"github.com/r13v/llmgate/internal/version"
	"github.com/r13v/llmgate/internal/wizard"
)

const usage = `llmgate configures Claude Code for a LiteLLM-compatible gateway.

Usage:
  llmgate [--help] [--version]
  llmgate update

With no arguments, llmgate starts the interactive setup wizard.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "update" {
		return runUpdateCommand(args[1:], stdout, stderr)
	}

	flags := flag.NewFlagSet("llmgate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	showHelp := flags.Bool("help", false, "show help")
	showVersion := flags.Bool("version", false, "show version")

	if err := flags.Parse(args); err != nil {
		_, _ = fmt.Fprintf(stderr, "llmgate: %s\n\n", sanitizeCLIError(err.Error()))
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}

	if flags.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "llmgate: unexpected argument %q\n\n", sanitizeCLIError(flags.Arg(0)))
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}

	switch {
	case *showHelp:
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	case *showVersion:
		_, _ = fmt.Fprint(stdout, version.Current().String())
		return 0
	default:
		if err := runWizardFn(stdout); err != nil {
			if errors.Is(err, wizard.ErrStartupDeclined) {
				return 0
			}
			_, _ = fmt.Fprintf(stderr, "llmgate: %s\n", sanitizeCLIError(err.Error()))
			return 1
		}
		return 0
	}
}

var runWizardFn = runWizard
var runUpdateFn = runUpdate

func runUpdateCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		_, _ = fmt.Fprintf(stderr, "llmgate: unexpected argument %q\n\n", sanitizeCLIError(args[0]))
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	if err := runUpdateFn(stdout, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "llmgate: %s\n", sanitizeCLIError(err.Error()))
		if install.IsUsageError(err) {
			return 2
		}
		return 1
	}
	return 0
}

func sanitizeCLIError(value string) string {
	home, _ := os.UserHomeDir()
	return redact.Text(value, redact.Options{HomeDir: home, GOOS: runtime.GOOS})
}

func runUpdate(stdout, _ io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return install.UsageError{Err: fmt.Errorf("locate home directory: %w", err)}
	}
	result, err := install.Update(context.Background(), install.Options{
		HomeDir:      home,
		LocalAppData: os.Getenv("LOCALAPPDATA"),
		XDGStateHome: os.Getenv("XDG_STATE_HOME"),
	})
	if err != nil {
		return err
	}

	switch {
	case result.AlreadyCurrent:
		_, _ = fmt.Fprintln(stdout, "llmgate is up to date")
	case result.Staged:
		_, _ = fmt.Fprintf(stdout, "Updated llmgate to main (%s); replacement will finish after this process exits\n", shortSHA(result.Metadata.ArchiveSHA256))
	default:
		_, _ = fmt.Fprintf(stdout, "Updated llmgate to main (%s)\n", shortSHA(result.Metadata.ArchiveSHA256))
	}
	return nil
}

func shortSHA(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func runWizard(stdout io.Writer) error {
	stdoutFile, _ := stdout.(*os.File)
	sys := system.NewRealSystem(os.Stdin, stdoutFile)
	err := wizard.Run(context.Background(), wizard.Options{
		System: sys,
		Input:  os.Stdin,
		Output: stdout,
	})
	if errors.Is(err, wizard.ErrNonInteractive) {
		return errors.New("no-argument setup requires an interactive terminal; run llmgate from a terminal")
	}
	return err
}
