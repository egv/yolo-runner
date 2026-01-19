package main

import (
	"os"
	"os/exec"

	"yolo-runner/internal/opencode"
)

func runCommand(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

type cmdProcess struct {
	cmd  *exec.Cmd
	file *os.File
}

func (process cmdProcess) Wait() error {
	err := process.cmd.Wait()
	if process.file != nil {
		_ = process.file.Close()
	}
	return err
}

func (process cmdProcess) Kill() error {
	if process.cmd.Process == nil {
		if process.file != nil {
			_ = process.file.Close()
		}
		return nil
	}
	err := process.cmd.Process.Kill()
	if process.file != nil {
		_ = process.file.Close()
	}
	return err
}

func startCommandWithEnv(args []string, env map[string]string, stdoutPath string) (opencode.Process, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	file, err := os.Create(stdoutPath)
	if err != nil {
		return nil, err
	}
	cmd.Stdout = file
	cmd.Stderr = file
	if err := cmd.Start(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return cmdProcess{cmd: cmd, file: file}, nil
}
