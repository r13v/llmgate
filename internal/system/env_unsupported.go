//go:build !windows

package system

type unsupportedWindowsUserEnvironment struct{}

func (unsupportedWindowsUserEnvironment) Snapshot([]string) (map[string]string, error) {
	return nil, ErrUnsupportedWindowsUserEnvironment
}

func (unsupportedWindowsUserEnvironment) Set(string, string) error {
	return ErrUnsupportedWindowsUserEnvironment
}
