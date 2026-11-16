//go:build unix

package core

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in its own session, so it outlives netbirdpod and the
// terminal it runs in.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
