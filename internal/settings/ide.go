package settings

import (
	"fmt"

	"github.com/r13v/llmgate/internal/core"
	"github.com/tailscale/hujson"
)

type IDE struct {
	Environment      map[string]string
	SelectedModel    string
	HasSelectedModel bool
}

func ParseIDE(data []byte) (IDE, error) {
	_, root, err := parseObjectRoot(data, "IDE", false)
	if err != nil {
		return IDE{}, err
	}

	parsed := IDE{Environment: make(map[string]string)}
	member, err := singleObjectMember(root, ideSelectedModelKey, "IDE")
	if err != nil {
		return IDE{}, err
	}
	if member != nil {
		if model, ok := stringValue(member.Value); ok {
			parsed.SelectedModel = model
			parsed.HasSelectedModel = true
		}
	}

	envMember, err := singleObjectMember(root, ideEnvironmentKey, "IDE")
	if err != nil {
		return IDE{}, err
	}
	if envMember == nil {
		return parsed, nil
	}

	envArray, ok := arrayValue(envMember.Value)
	if !ok {
		return IDE{}, malformed("IDE", fmt.Sprintf("%q must be an array", ideEnvironmentKey))
	}

	for i := range envArray.Elements {
		entry, ok := objectValue(envArray.Elements[i])
		if !ok {
			continue
		}
		nameMember := firstObjectMember(entry, "name")
		valueMember := firstObjectMember(entry, "value")
		if nameMember == nil || valueMember == nil {
			continue
		}
		name, nameOK := stringValue(nameMember.Value)
		value, valueOK := stringValue(valueMember.Value)
		if nameOK && valueOK && core.IsManaged(name) {
			parsed.Environment[name] = value
		}
	}

	return parsed, nil
}

func UpsertIDE(data []byte, selectedModel string, values map[string]string) ([]byte, error) {
	doc, root, err := parseObjectRoot(data, "IDE", true)
	if err != nil {
		return nil, err
	}

	if _, err := singleObjectMember(root, ideSelectedModelKey, "IDE"); err != nil {
		return nil, err
	}
	changed := upsertObjectString(root, ideSelectedModelKey, selectedModel)

	envArray, envChanged, err := ensureArrayMember(root, ideEnvironmentKey, "IDE")
	if err != nil {
		return nil, err
	}
	changed = changed || envChanged

	present, entriesChanged := updateIDEEnvironmentEntries(envArray, values)
	changed = changed || entriesChanged
	for _, name := range orderedNames(values) {
		if present[name] {
			continue
		}
		envArray.Elements = append(envArray.Elements, newIDEEnvironmentEntry(name, values[name]))
		changed = true
	}

	if !changed && data != nil {
		return append([]byte(nil), data...), nil
	}

	return packFormatted(&doc), nil
}

func ensureArrayMember(root *hujson.Object, name, label string) (*hujson.Array, bool, error) {
	changed := false
	member, err := singleObjectMember(root, name, label)
	if err != nil {
		return nil, false, err
	}
	if member == nil {
		member = upsertObjectValue(root, name, newArrayValue())
		changed = true
	}
	array, ok := arrayValue(member.Value)
	if !ok {
		return nil, false, malformed(label, fmt.Sprintf("%q must be an array", name))
	}
	return array, changed, nil
}

func updateIDEEnvironmentEntries(envArray *hujson.Array, values map[string]string) (map[string]bool, bool) {
	present := make(map[string]bool, len(values))
	changed := false
	for i := range envArray.Elements {
		entry, ok := objectValue(envArray.Elements[i])
		if !ok {
			continue
		}
		nameMember := firstObjectMember(entry, "name")
		if nameMember == nil {
			continue
		}
		name, ok := stringValue(nameMember.Value)
		if !ok {
			continue
		}
		value, ok := values[name]
		if !ok {
			continue
		}
		changed = upsertObjectString(entry, "value", value) || changed
		present[name] = true
	}
	return present, changed
}

func newIDEEnvironmentEntry(name, value string) hujson.Value {
	return hujson.Value{
		Value: &hujson.Object{
			Members: []hujson.ObjectMember{
				stringMember("name", name),
				stringMember("value", value),
			},
		},
	}
}
