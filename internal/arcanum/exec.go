package arcanum

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type arcExecFunc func(ctx context.Context, workspace string, name string, args ...string) ([]byte, []byte, error)

var arcExec arcExecFunc = defaultArcExec

func RunWorkspaceArc(ctx context.Context, workspace string, args ...string) ([]byte, error) {
	stdout, stderr, err := arcExec(ctx, workspace, "arc", args...)
	if err != nil {
		return nil, workspaceArcError(workspace, args, stderr, err)
	}
	return stdout, nil
}

func defaultArcExec(ctx context.Context, workspace string, name string, args ...string) ([]byte, []byte, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workspace
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return append([]byte(nil), stdout.Bytes()...), append([]byte(nil), stderr.Bytes()...), err
}

func workspaceArcError(workspace string, args []string, stderr []byte, err error) error {
	command := strings.Join(append([]string{"arc"}, args...), " ")
	details := strings.TrimSpace(string(stderr))
	if details == "" {
		return fmt.Errorf("%s in workspace %s failed: %w", command, workspace, err)
	}
	return fmt.Errorf("%s in workspace %s failed: %s: %w", command, workspace, details, err)
}
