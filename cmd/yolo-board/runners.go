package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func renderRunnersTab(snapshot boardSnapshot, now time.Time) string {
	var b strings.Builder
	b.WriteString("Runners\n")
	b.WriteString("ID\tPID\tPRESETS\tCAP\tHEARTBEAT\tCURRENT\n")
	for _, runner := range snapshot.runners {
		fmt.Fprintf(
			&b,
			"%s\t%d\t%s\t%d\t%s\t%s\n",
			runner.ID,
			runner.Pid,
			runner.Presets,
			runner.Capacity,
			formatRunnerHeartbeatAge(runner, now),
			formatRunnerCurrentItem(snapshot.currentByRunner[runner.ID]),
		)
	}
	return b.String()
}

func formatRunnerHeartbeatAge(runner workqueue.RunnerRow, now time.Time) string {
	if runner.HeartbeatAt.IsZero() {
		return "-"
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	age := now.Sub(runner.HeartbeatAt)
	if age < 0 {
		age = 0
	}
	return age.Round(time.Second).String()
}

func formatRunnerCurrentItem(item *workitem.Item) string {
	if item == nil {
		return "-"
	}
	return fmt.Sprintf("%s %s/%s", item.Kind, item.Source, item.SourceRef)
}
