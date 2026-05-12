//go:build darwin || linux

package system

func NewWindowsUserEnvironment() WindowsUserEnvironment {
	return unsupportedWindowsUserEnvironment{}
}
