package arcanum

import (
	"context"
	"fmt"
	"strings"
)

type ShipArcanumClient struct {
	workspace string
}

func NewShipArcanumClient(workspace string) *ShipArcanumClient {
	return &ShipArcanumClient{workspace: strings.TrimSpace(workspace)}
}

func (c *ShipArcanumClient) Ship(ctx context.Context, prID string) error {
	if c == nil {
		return fmt.Errorf("ship Arcanum client is nil")
	}
	workspace := strings.TrimSpace(c.workspace)
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}
	prID = strings.TrimSpace(prID)
	if prID == "" {
		return fmt.Errorf("PR ID is required")
	}

	_, err := RunWorkspaceArc(ctx, workspace, "pr", "merge", "--now", prID)
	return err
}
