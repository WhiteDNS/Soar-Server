//go:build !linux

package panelops

import "os/exec"

func dropPrivileges(*exec.Cmd, int, int) {}
