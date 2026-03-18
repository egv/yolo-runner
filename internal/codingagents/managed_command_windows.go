//go:build windows

package codingagents

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func configureManagedCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
}

func stopManagedCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid))
	if err == nil || errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
		return nil
	}

	signalErr := cmd.Process.Signal(os.Interrupt)
	if signalErr == nil || errors.Is(signalErr, os.ErrProcessDone) {
		return nil
	}
	return errors.Join(err, signalErr)
}

func killManagedCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func attachManagedCommand(proc *managedCommandProcess) error {
	if proc == nil || proc.cmd == nil || proc.cmd.Process == nil {
		return nil
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}

	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}

	processHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(proc.cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return err
	}
	defer windows.CloseHandle(processHandle)

	if err := windows.AssignProcessToJobObject(job, processHandle); err != nil {
		_ = windows.CloseHandle(job)
		return err
	}

	proc.cleanupFn = func() {
		_ = windows.CloseHandle(job)
	}
	proc.killFn = func(_ *exec.Cmd) error {
		err := windows.TerminateJobObject(job, 1)
		if err == nil || errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		if errors.Is(err, windows.ERROR_INVALID_HANDLE) {
			return nil
		}
		return err
	}
	return nil
}
