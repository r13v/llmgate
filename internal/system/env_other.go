//go:build !darwin && !linux && !windows

package system

func NewWindowsUserEnvironment() WindowsUserEnvironment {
	return unsupportedWindowsUserEnvironment{}
}
