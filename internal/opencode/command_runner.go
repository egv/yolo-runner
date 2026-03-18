package opencode

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type CommandRunner struct{}

type commandProcess struct {
	cmd        *exec.Cmd
	stderrFile *os.File
	stdin      io.WriteCloser
	stdout     io.ReadCloser
}

func (commandProcess) ensure() {}

func (p commandProcess) Stdin() io.WriteCloser { return p.stdin }

func (p commandProcess) Stdout() io.ReadCloser { return p.stdout }

func (p commandProcess) Wait() error {
	err := p.cmd.Wait()
	if p.stderrFile != nil {
		_ = p.stderrFile.Close()
	}
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.stdout != nil {
		_ = p.stdout.Close()
	}
	return err
}

func (p commandProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	err := p.cmd.Process.Kill()
	if p.stderrFile != nil {
		_ = p.stderrFile.Close()
	}
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.stdout != nil {
		_ = p.stdout.Close()
	}
	return err
}

func (CommandRunner) Start(args []string, env map[string]string, stdoutPath string) (Process, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	if err := os.MkdirAll(filepath.Dir(stdoutPath), 0o755); err != nil {
		return nil, err
	}

	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return nil, err
	}
	_ = stdoutFile.Close()

	stderrPath := contracts.BackendLogSidecarPath(stdoutPath, contracts.BackendLogStderr)
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return nil, err
	}
	cmd.Stderr = stderrFile

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stderrFile.Close()
		return nil, err
	}
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		_ = stdoutPipe.Close()
		_ = stderrFile.Close()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		_ = stderrFile.Close()
		return nil, err
	}

	return commandProcess{cmd: cmd, stderrFile: stderrFile, stdin: stdinPipe, stdout: stdoutPipe}, nil
}
