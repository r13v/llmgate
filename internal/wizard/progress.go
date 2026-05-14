package wizard

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/gateway"
	"github.com/r13v/llmgate/internal/system"
	"golang.org/x/term"
)

type progressReporter struct {
	out     io.Writer
	animate bool
	log     bool
	color   bool
}

func newProgressReporter(out io.Writer, env system.ProcessEnvironment, accessible bool) progressReporter {
	terminal := outputIsTerminal(out)
	return progressReporter{
		out:     out,
		animate: terminal && !accessible && supportsTerminalControl(env),
		log:     terminal,
		color:   shouldUseANSI(out, env, accessible),
	}
}

func (p progressReporter) Run(message string, fn func() error) error {
	if !p.animate {
		if p.log {
			_, _ = fmt.Fprintf(p.out, "%s %s\n", colorMessageType("INFO", p.color), message)
		}
		return fn()
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		frames := []string{"|", "/", "-", "\\"}
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			_, _ = fmt.Fprintf(p.out, "\r%s %s", colorMessageType(frames[i%len(frames)], p.color), message)
			i++
			select {
			case <-stop:
				_, _ = fmt.Fprint(p.out, "\r\x1b[2K")
				return
			case <-ticker.C:
			}
		}
	}()

	err := fn()
	close(stop)
	<-done
	return err
}

func gatewayModelListProgressMessage(baseURL, token string, display displayOptions) string {
	modelURLs, err := gateway.NormalizeModelURLs(baseURL)
	if err != nil {
		return "Checking gateway model list for " + sanitizeText(baseURL, []string{token}, display) + "."
	}
	return fmt.Sprintf(
		"Checking gateway model list: %s (fallback %s).",
		sanitizeText(modelURLs.Primary, []string{token}, display),
		sanitizeText(modelURLs.Fallback, []string{token}, display),
	)
}

func gatewayProbeProgressMessage(baseURL, token, model string, display displayOptions) string {
	completionsURL, err := gateway.NormalizeCompletionsURL(baseURL)
	if err != nil {
		return "Probing selected model " + sanitizeText(model, []string{token}, display) + "."
	}
	return fmt.Sprintf(
		"Probing selected model %s via %s.",
		sanitizeText(model, []string{token}, display),
		sanitizeText(completionsURL, []string{token}, display),
	)
}

func shouldUseANSI(out io.Writer, env system.ProcessEnvironment, accessible bool) bool {
	if accessible || !outputIsTerminal(out) {
		return false
	}
	if !supportsTerminalControl(env) {
		return false
	}
	if env != nil {
		if _, ok := env.LookupEnv("NO_COLOR"); ok {
			return false
		}
	}
	return true
}

func supportsTerminalControl(env system.ProcessEnvironment) bool {
	if env == nil {
		return true
	}
	return !strings.EqualFold(env.Getenv("TERM"), "dumb")
}

func outputIsTerminal(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok || file == nil {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

func colorStatus(status core.DiagnosticStatus, color bool) string {
	text := status.String()
	if !color {
		return text
	}
	switch status {
	case core.StatusOK:
		return ansi("32", text)
	case core.StatusSKIP:
		return ansi("36", text)
	case core.StatusWARN:
		return ansi("33", text)
	case core.StatusFAIL:
		return ansi("31", text)
	default:
		return text
	}
}

func colorMessageType(kind string, color bool) string {
	if !color {
		return kind
	}
	switch kind {
	case "INFO":
		return ansi("36", kind)
	case "WARN":
		return ansi("33", kind)
	case "FAIL":
		return ansi("31", kind)
	default:
		return ansi("36", kind)
	}
}

func ansi(code, text string) string {
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}
