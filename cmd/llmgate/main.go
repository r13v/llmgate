package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/r13v/llmgate/internal/redact"
	"github.com/r13v/llmgate/internal/system"
	"github.com/r13v/llmgate/internal/version"
	"github.com/r13v/llmgate/internal/wizard"
)

const usage = `llmgate configures Claude Code for a LiteLLM-compatible gateway.

Usage:
  llmgate [--help] [--version]

With no arguments, llmgate starts the interactive setup wizard.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
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
		if err := runWizard(stdout); err != nil {
			_, _ = fmt.Fprintf(stderr, "llmgate: %s\n", sanitizeCLIError(err.Error()))
			return 1
		}
		return 0
	}
}

func sanitizeCLIError(value string) string {
	return redact.Text(value, redact.Options{})
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
