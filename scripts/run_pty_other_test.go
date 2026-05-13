//go:build !unix

package scripts

import "syscall"

func pipedScriptSysProcAttr() *syscall.SysProcAttr {
	return nil
}
