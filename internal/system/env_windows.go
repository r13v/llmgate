//go:build windows

package system

import "golang.org/x/sys/windows/registry"

const userEnvironmentRegistryPath = `Environment`

type registryWindowsUserEnvironment struct{}

func NewWindowsUserEnvironment() WindowsUserEnvironment {
	return registryWindowsUserEnvironment{}
}

func (registryWindowsUserEnvironment) Lookup(name string) (string, bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, userEnvironmentRegistryPath, registry.QUERY_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return "", false, nil
		}
		return "", false, err
	}
	defer func() {
		_ = key.Close()
	}()

	value, _, err := key.GetStringValue(name)
	if err != nil {
		if err == registry.ErrNotExist {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

func (env registryWindowsUserEnvironment) Snapshot(names []string) (map[string]string, error) {
	values := make(map[string]string, len(names))
	for _, name := range names {
		value, ok, err := env.Lookup(name)
		if err != nil {
			return nil, err
		}
		if ok {
			values[name] = value
		}
	}
	return values, nil
}

func (registryWindowsUserEnvironment) Set(name, value string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, userEnvironmentRegistryPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer func() {
		_ = key.Close()
	}()
	return key.SetStringValue(name, value)
}

func (registryWindowsUserEnvironment) Delete(name string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, userEnvironmentRegistryPath, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	defer func() {
		_ = key.Close()
	}()

	err = key.DeleteValue(name)
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}
