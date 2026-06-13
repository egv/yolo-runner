package main

import (
	"context"
	"fmt"
	"os"
)

const arcReviewWatchDeprecationNotice = "arc-review-watch is deprecated; use `yolo-agent source arcpr` instead."

type arcReviewDiscoveredPR struct {
	ID        string
	Workspace string
	Branch    string
	Revision  string
}

type arcReviewWatchConfigResolver interface {
	ResolveArcReviewWatchConfig(repoRoot string) (arcReviewWatchConfig, error)
}

func defaultRunArcReviewWatch(ctx context.Context, cfg arcReviewWatchCommandConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	fmt.Fprintln(os.Stderr, arcReviewWatchDeprecationNotice)
	if cfg.dryRun {
		fmt.Fprintln(os.Stderr, "warning: --dry-run is ignored by the arc-review-watch compatibility shim")
	}

	handler := runSourceArcPR
	if handler == nil {
		handler = defaultRunSourceArcPR
	}
	return handler(ctx, sourceArcPRCommandConfig{
		repoRoot:   cfg.repoRoot,
		profile:    cfg.profile,
		once:       cfg.once,
		stream:     cfg.stream,
		eventsPath: cfg.eventsPath,
		eventSink:  cfg.eventSink,
	})
}
