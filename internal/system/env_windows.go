//go:build windows

package system

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const userEnvironmentRegistryPath = `Environment`
const (
	hwndBroadcast     = 0xffff
	wmSettingChange   = 0x001A
	smtoAbortIfHung   = 0x0002
	broadcastTimeout  = 5000
	environmentChange = "Environment"
)

var procSendMessageTimeoutW = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")

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
	if err := key.SetStringValue(name, value); err != nil {
		return err
	}
	return broadcastEnvironmentChange()
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
	if err != nil {
		return err
	}
	return broadcastEnvironmentChange()
}

func broadcastEnvironmentChange() error {
	message, err := windows.UTF16PtrFromString(environmentChange)
	if err != nil {
		return err
	}
	var result uintptr
	r1, _, callErr := procSendMessageTimeoutW.Call(
		uintptr(hwndBroadcast),
		uintptr(wmSettingChange),
		0,
		uintptr(unsafe.Pointer(message)),
		uintptr(smtoAbortIfHung),
		uintptr(broadcastTimeout),
		uintptr(unsafe.Pointer(&result)),
	)
	if r1 == 0 {
		return fmt.Errorf("broadcast Windows environment change: %w", callErr)
	}
	return nil
}
