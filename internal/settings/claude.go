package settings

import (
	"fmt"

	"github.com/r13v/llmgate/internal/core"
	"github.com/tailscale/hujson"
)

type Claude struct {
	Env map[string]string
}

func ParseClaude(data []byte) (Claude, error) {
	_, root, err := parseObjectRoot(data, "Claude", false)
	if err != nil {
		return Claude{}, err
	}

	env := make(map[string]string)
	member := firstObjectMember(root, claudeEnvKey)
	if member == nil {
		return Claude{Env: env}, nil
	}

	envObj, ok := objectValue(member.Value)
	if !ok {
		return Claude{}, malformed("Claude", fmt.Sprintf("%q must be an object", claudeEnvKey))
	}

	for i := range envObj.Members {
		name, ok := stringValue(envObj.Members[i].Name)
		if !ok || !core.IsManaged(name) {
			continue
		}
		value, ok := stringValue(envObj.Members[i].Value)
		if ok {
			env[name] = value
		}
	}

	return Claude{Env: env}, nil
}

func UpsertClaude(data []byte, values map[string]string) ([]byte, error) {
	doc, root, err := parseObjectRoot(data, "Claude", true)
	if err != nil {
		return nil, err
	}

	envObj, changed, err := ensureObjectMember(root, claudeEnvKey, "Claude")
	if err != nil {
		return nil, err
	}

	for _, name := range orderedNames(values) {
		changed = upsertObjectString(envObj, name, values[name]) || changed
	}

	if !changed && data != nil {
		return append([]byte(nil), data...), nil
	}

	return packFormatted(&doc), nil
}

func ensureObjectMember(root *hujson.Object, name, label string) (*hujson.Object, bool, error) {
	changed := false
	member := firstObjectMember(root, name)
	if member == nil {
		member = upsertObjectValue(root, name, newObjectValue())
		changed = true
	}
	obj, ok := objectValue(member.Value)
	if !ok {
		return nil, false, malformed(label, fmt.Sprintf("%q must be an object", name))
	}
	return obj, changed, nil
}
