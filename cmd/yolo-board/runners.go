package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
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

func renderRunnerDetail(snapshot boardSnapshot, events []contracts.Event, runnerID string, now time.Time) string {
	runner, ok := findRunner(snapshot.runners, runnerID)
	if !ok {
		return renderRunnersTab(snapshot, now)
	}

	current := snapshot.currentByRunner[runner.ID]
	var b strings.Builder
	fmt.Fprintf(&b, "Runner %s\n", runner.ID)
	b.WriteString("Registration\n")
	b.WriteString("ID\tPID\tPRESETS\tCAP\tHEARTBEAT\n")
	fmt.Fprintf(
		&b,
		"%s\t%d\t%s\t%d\t%s\n",
		runner.ID,
		runner.Pid,
		runner.Presets,
		runner.Capacity,
		formatRunnerHeartbeatAge(runner, now),
	)

	b.WriteString("\nCurrent item\n")
	if current == nil {
		b.WriteString("-\n")
	} else {
		fmt.Fprintf(&b, "%s\t%s\t%s\n", current.ID, current.State, formatRunnerCurrentItem(current))
	}

	b.WriteString("\nLive activity\n")
	liveCount := 0
	for _, event := range events {
		if !eventMatchesRunner(event, runner.ID) || !eventMatchesCurrentItem(event, current) {
			continue
		}
		switch event.Type {
		case contracts.EventTypeAgentText:
			fmt.Fprintf(&b, "runner_output\t%s\n", formatEventMessage(event))
			liveCount++
		case contracts.EventTypeAgentProgress:
			fmt.Fprintf(&b, "runner_progress\t%s\n", formatEventMessage(event))
			liveCount++
		}
	}
	if liveCount == 0 {
		b.WriteString("-\n")
	}

	b.WriteString("\nRecent finishes\n")
	finishCount := 0
	for _, event := range events {
		if !eventMatchesRunner(event, runner.ID) || event.Type != contracts.EventTypeAgentFinished {
			continue
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\n", formatEventItemID(event), formatEventSourceRef(event), formatEventMessage(event))
		finishCount++
	}
	if finishCount == 0 {
		b.WriteString("-\n")
	}
	return b.String()
}

func findRunner(runners []workqueue.RunnerRow, runnerID string) (workqueue.RunnerRow, bool) {
	for _, runner := range runners {
		if runner.ID == runnerID {
			return runner, true
		}
	}
	return workqueue.RunnerRow{}, false
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

func eventMatchesRunner(event contracts.Event, runnerID string) bool {
	return event.RunnerID == runnerID || event.Proc == runnerID || event.WorkerID == runnerID
}

func eventMatchesCurrentItem(event contracts.Event, current *workitem.Item) bool {
	if current == nil {
		return true
	}
	if event.ItemID != "" {
		return event.ItemID == current.ID
	}
	if event.TaskID != "" {
		return event.TaskID == current.ID || event.TaskID == current.SourceRef
	}
	return event.Source == current.Source && event.SourceRef == current.SourceRef
}

func formatEventItemID(event contracts.Event) string {
	if event.ItemID != "" {
		return event.ItemID
	}
	if event.TaskID != "" {
		return event.TaskID
	}
	return "-"
}

func formatEventSourceRef(event contracts.Event) string {
	if event.Source != "" || event.SourceRef != "" {
		return strings.Trim(strings.Join([]string{event.Source, event.SourceRef}, "/"), "/")
	}
	return "-"
}

func formatEventMessage(event contracts.Event) string {
	if event.Message != "" {
		return event.Message
	}
	if event.Detail != "" {
		return event.Detail
	}
	return "-"
}
