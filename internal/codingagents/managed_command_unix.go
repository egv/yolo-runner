//go:build !windows

package codingagents

import (
	"errors"
	"os/exec"
	"syscall"
)

func configureManagedCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func stopManagedCommand(cmd *exec.Cmd) error {
	return signalManagedCommandGroup(cmd, syscall.SIGINT)
}

func killManagedCommand(cmd *exec.Cmd) error {
	return signalManagedCommandGroup(cmd, syscall.SIGKILL)
}

func attachManagedCommand(_ *managedCommandProcess) error {
	return nil
}

func signalManagedCommandGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
