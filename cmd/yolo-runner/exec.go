package main

import (
	"os"
	"os/exec"
)

func runCommand(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func runCommandWithEnv(args []string, env map[string]string, stdoutPath string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	file, err := os.Create(stdoutPath)
	if err != nil {
		return err
	}
	defer file.Close()
	cmd.Stdout = file
	cmd.Stderr = file
	return cmd.Run()
}
