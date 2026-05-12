package wizard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"charm.land/huh/v2"
)

var ErrCanceled = errors.New("prompt canceled")

type Option struct {
	Label    string
	Value    string
	Selected bool
}

type ConfirmPrompt struct {
	Title       string
	Description string
	Affirmative string
	Negative    string
	Default     bool
}

type InputPrompt struct {
	Title       string
	Description string
	Placeholder string
	Default     string
	Required    bool
	Secret      bool
}

type SelectPrompt struct {
	Title       string
	Description string
	Options     []Option
	Default     string
}

type MultiSelectPrompt struct {
	Title       string
	Description string
	Options     []Option
}

type Prompter interface {
	Confirm(context.Context, ConfirmPrompt) (bool, error)
	Input(context.Context, InputPrompt) (string, error)
	Select(context.Context, SelectPrompt) (string, error)
	MultiSelect(context.Context, MultiSelectPrompt) ([]string, error)
}

type HuhPrompter struct {
	In         io.Reader
	Output     io.Writer
	Accessible bool
}

func (p HuhPrompter) Confirm(ctx context.Context, prompt ConfirmPrompt) (bool, error) {
	value := prompt.Default
	field := huh.NewConfirm().
		Title(prompt.Title).
		Description(prompt.Description).
		Value(&value)
	if prompt.Affirmative != "" {
		field = field.Affirmative(prompt.Affirmative)
	}
	if prompt.Negative != "" {
		field = field.Negative(prompt.Negative)
	}
	if err := p.run(ctx, field); err != nil {
		return false, err
	}
	return value, nil
}

func (p HuhPrompter) Input(ctx context.Context, prompt InputPrompt) (string, error) {
	value := prompt.Default
	field := huh.NewInput().
		Title(prompt.Title).
		Description(prompt.Description).
		Placeholder(prompt.Placeholder).
		Value(&value).
		Validate(func(value string) error {
			if prompt.Required && strings.TrimSpace(value) == "" {
				return errors.New("value required")
			}
			return nil
		})
	if prompt.Secret && !p.Accessible {
		field = field.EchoMode(huh.EchoModePassword)
	}
	if err := p.run(ctx, field); err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func (p HuhPrompter) Select(ctx context.Context, prompt SelectPrompt) (string, error) {
	if len(prompt.Options) == 0 {
		return "", errors.New("select prompt requires at least one option")
	}
	value := prompt.Default
	if value == "" || !optionValueExists(prompt.Options, value) {
		value = prompt.Options[0].Value
	}
	options := make([]huh.Option[string], 0, len(prompt.Options))
	for _, option := range prompt.Options {
		options = append(options, huh.NewOption(option.Label, option.Value).Selected(option.Selected || option.Value == value))
	}
	field := huh.NewSelect[string]().
		Title(prompt.Title).
		Description(prompt.Description).
		Options(options...).
		Value(&value)
	if err := p.run(ctx, field); err != nil {
		return "", err
	}
	return value, nil
}

func (p HuhPrompter) MultiSelect(ctx context.Context, prompt MultiSelectPrompt) ([]string, error) {
	options := make([]huh.Option[string], 0, len(prompt.Options))
	selected := make([]string, 0, len(prompt.Options))
	for _, option := range prompt.Options {
		options = append(options, huh.NewOption(option.Label, option.Value).Selected(option.Selected))
		if option.Selected {
			selected = append(selected, option.Value)
		}
	}
	field := huh.NewMultiSelect[string]().
		Title(prompt.Title).
		Description(prompt.Description).
		Value(&selected).
		Options(options...)
	if err := p.run(ctx, field); err != nil {
		return nil, err
	}
	return selected, nil
}

func (p HuhPrompter) run(ctx context.Context, field huh.Field) error {
	output := p.Output
	if output == nil {
		output = io.Discard
	}
	form := huh.NewForm(huh.NewGroup(field)).
		WithInput(p.In).
		WithOutput(output).
		WithAccessible(p.Accessible)
	if err := form.RunWithContext(ctx); err != nil {
		if isCancelError(err) {
			return ErrCanceled
		}
		return err
	}
	return nil
}

func isCancelError(err error) bool {
	return errors.Is(err, ErrCanceled) ||
		errors.Is(err, huh.ErrUserAborted) ||
		errors.Is(err, context.Canceled)
}

func optionValueExists(options []Option, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}

func selectOptions(values []string) []Option {
	options := make([]Option, 0, len(values))
	for _, value := range values {
		options = append(options, Option{Label: value, Value: value})
	}
	return options
}

func numberedValue(index int) string {
	return fmt.Sprintf("%d", index)
}
