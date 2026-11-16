//go:build !unix

package core

import "os/exec"

func detach(*exec.Cmd) {}
