//go:build linux

package panelops

import (
	"os"
	"os/exec"
	"syscall"
)

func dropPrivileges(cmd *exec.Cmd, uid, gid int) {
	if uid > 0 && os.Geteuid() == 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
	}
}
