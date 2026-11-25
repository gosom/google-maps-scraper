package mcpserver

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func detach(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
}
