//go:build !windows

package update

import (
	"os/exec"
	"syscall"
)

// detach puts the helper in its own session so it survives this process exiting.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
