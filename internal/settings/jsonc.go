package settings

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/r13v/llmgate/internal/core"
	"github.com/r13v/llmgate/internal/redact"
	"github.com/tailscale/hujson"
)

const (
	claudeEnvKey        = "env"
	ideEnvironmentKey   = "claudeCode.environmentVariables"
	ideSelectedModelKey = "claudeCode.selectedModel"
)

func parseObjectRoot(data []byte, label string, allowMissing bool) (hujson.Value, *hujson.Object, error) {
	if data == nil && allowMissing {
		obj := &hujson.Object{}
		return hujson.Value{Value: obj}, obj, nil
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return hujson.Value{}, nil, fmt.Errorf("%s settings JSONC is empty", label)
	}

	value, err := hujson.Parse(data)
	if err != nil {
		return hujson.Value{}, nil, fmt.Errorf("parse %s settings JSONC: %s", label, redactedError(err))
	}

	obj, ok := value.Value.(*hujson.Object)
	if !ok {
		return hujson.Value{}, nil, fmt.Errorf("%s settings root must be an object", label)
	}
	return value, obj, nil
}

func redactedError(err error) string {
	if err == nil {
		return ""
	}
	return redact.Text(err.Error(), redact.Options{})
}

func stringValue(value hujson.Value) (string, bool) {
	literal, ok := value.Value.(hujson.Literal)
	if !ok || literal.Kind() != '"' {
		return "", false
	}
	return literal.String(), true
}

func objectValue(value hujson.Value) (*hujson.Object, bool) {
	obj, ok := value.Value.(*hujson.Object)
	return obj, ok
}

func arrayValue(value hujson.Value) (*hujson.Array, bool) {
	array, ok := value.Value.(*hujson.Array)
	return array, ok
}

func findObjectMembers(obj *hujson.Object, name string) []*hujson.ObjectMember {
	matches := make([]*hujson.ObjectMember, 0, 1)
	for i := range obj.Members {
		memberName, ok := stringValue(obj.Members[i].Name)
		if ok && memberName == name {
			matches = append(matches, &obj.Members[i])
		}
	}
	return matches
}

func firstObjectMember(obj *hujson.Object, name string) *hujson.ObjectMember {
	matches := findObjectMembers(obj, name)
	if len(matches) == 0 {
		return nil
	}
	return matches[0]
}

func upsertObjectString(obj *hujson.Object, name, value string) bool {
	matches := findObjectMembers(obj, name)
	if len(matches) == 0 {
		obj.Members = append(obj.Members, stringMember(name, value))
		return true
	}

	changed := false
	for _, member := range matches {
		if existing, ok := stringValue(member.Value); ok && existing == value {
			continue
		}
		setString(&member.Value, value)
		changed = true
	}
	return changed
}

func upsertObjectValue(obj *hujson.Object, name string, value hujson.Value) *hujson.ObjectMember {
	matches := findObjectMembers(obj, name)
	if len(matches) == 0 {
		obj.Members = append(obj.Members, hujson.ObjectMember{
			Name:  newStringValue(name),
			Value: value,
		})
		return &obj.Members[len(obj.Members)-1]
	}
	for _, member := range matches {
		before := member.Value.BeforeExtra
		after := member.Value.AfterExtra
		member.Value = value.Clone()
		member.Value.BeforeExtra = before
		member.Value.AfterExtra = after
	}
	return matches[0]
}

func stringMember(name, value string) hujson.ObjectMember {
	return hujson.ObjectMember{
		Name:  newStringValue(name),
		Value: newStringValue(value),
	}
}

func newStringValue(value string) hujson.Value {
	return hujson.Value{Value: hujson.String(value)}
}

func newObjectValue() hujson.Value {
	return hujson.Value{Value: &hujson.Object{}}
}

func newArrayValue() hujson.Value {
	return hujson.Value{Value: &hujson.Array{}}
}

func setString(value *hujson.Value, text string) {
	before := value.BeforeExtra
	after := value.AfterExtra
	*value = newStringValue(text)
	value.BeforeExtra = before
	value.AfterExtra = after
}

func packFormatted(value *hujson.Value) []byte {
	value.Format()
	return value.Pack()
}

func orderedNames(values map[string]string) []string {
	names := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, name := range core.AllManagedNames() {
		if _, ok := values[name]; ok {
			names = append(names, name)
			seen[name] = struct{}{}
		}
	}

	extras := make([]string, 0)
	for name := range values {
		if _, ok := seen[name]; ok {
			continue
		}
		extras = append(extras, name)
	}
	sort.Strings(extras)
	names = append(names, extras...)
	return names
}

func malformed(label, detail string) error {
	if detail == "" {
		return errors.New(label + " settings are malformed")
	}
	return fmt.Errorf("%s settings are malformed: %s", label, detail)
}
