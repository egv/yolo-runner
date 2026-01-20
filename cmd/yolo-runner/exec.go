package main

import (
	"os"
	"os/exec"
	"strings"

	"yolo-runner/internal/opencode"
)

func runCommand(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

type cmdProcess struct {
	cmd        *exec.Cmd
	stdoutFile *os.File
	stderrFile *os.File
}

func (process cmdProcess) Wait() error {
	err := process.cmd.Wait()
	if process.stdoutFile != nil {
		_ = process.stdoutFile.Close()
	}
	if process.stderrFile != nil {
		_ = process.stderrFile.Close()
	}
	return err
}

func (process cmdProcess) Kill() error {
	if process.cmd.Process == nil {
		if process.stdoutFile != nil {
			_ = process.stdoutFile.Close()
		}
		if process.stderrFile != nil {
			_ = process.stderrFile.Close()
		}
		return nil
	}
	err := process.cmd.Process.Kill()
	if process.stdoutFile != nil {
		_ = process.stdoutFile.Close()
	}
	if process.stderrFile != nil {
		_ = process.stderrFile.Close()
	}
	return err
}

func startCommandWithEnv(args []string, env map[string]string, stdoutPath string) (opencode.Process, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return nil, err
	}
	stderrPath := strings.TrimSuffix(stdoutPath, ".jsonl") + ".stderr.log"
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		_ = stdoutFile.Close()
		return nil, err
	}
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	if err := cmd.Start(); err != nil {
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
		return nil, err
	}
	return cmdProcess{cmd: cmd, stdoutFile: stdoutFile, stderrFile: stderrFile}, nil
}
