package arcanum

import (
	"context"
	"fmt"
	"strings"
)

func ListIncomingReviewPRs(ctx context.Context) ([]PRSummary, error) {
	stdout, err := runArc(ctx, "pr", "list", "--json", "-i", "--status", "open")
	if err != nil {
		return nil, err
	}
	return ParsePRListJSON(stdout)
}

func runArc(ctx context.Context, args ...string) ([]byte, error) {
	stdout, stderr, err := arcExec(ctx, "", "arc", args...)
	if err != nil {
		return nil, arcCommandError(args, stderr, err)
	}
	return stdout, nil
}

func arcCommandError(args []string, stderr []byte, err error) error {
	command := strings.Join(append([]string{"arc"}, args...), " ")
	details := strings.TrimSpace(string(stderr))
	if details == "" {
		return fmt.Errorf("%s failed: %w", command, err)
	}
	return fmt.Errorf("%s failed: %s: %w", command, details, err)
}
