package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/workitem"
	"github.com/egv/yolo-runner/v2/internal/workqueue"
)

func renderQueueTab(snapshot boardSnapshot, now time.Time) string {
	var b strings.Builder
	b.WriteString("Queue\n")
	fmt.Fprintf(&b, "Counts: %s\n", formatQueueStateCounts(snapshot.stateCounts))
	b.WriteString("KIND\tSOURCE_REF\tPRESET\tPRIORITY\tSTATE\tATTEMPT\tCLAIMED_BY\tAGE\n")

	items := sortedQueueItems(snapshot.items)

	for _, item := range items {
		fmt.Fprintf(
			&b,
			"%s\t%s\t%s\t%d\t%s\t%d\t%s\t%s\n",
			item.Kind,
			item.SourceRef,
			item.Preset,
			item.Priority,
			item.State,
			item.Attempt,
			formatQueueClaimedBy(item.ClaimedBy),
			formatQueueItemAge(item, now),
		)
	}
	return b.String()
}

func renderQueueItemDetail(detail workqueue.ItemDetail, events []contracts.Event) string {
	item := detail.Item
	var b strings.Builder
	fmt.Fprintf(&b, "Item %s\n", item.ID)

	b.WriteString("Fields\n")
	b.WriteString("ID\tKIND\tSOURCE\tSOURCE_REF\tPRESET\tPRIORITY\tSTATE\tATTEMPT\tCLAIMED_BY\n")
	fmt.Fprintf(
		&b,
		"%s\t%s\t%s\t%s\t%s\t%d\t%s\t%d\t%s\n",
		item.ID,
		item.Kind,
		item.Source,
		item.SourceRef,
		item.Preset,
		item.Priority,
		item.State,
		item.Attempt,
		formatQueueClaimedBy(item.ClaimedBy),
	)
	b.WriteString("IDEMPOTENCY_KEY\tMAX_ATTEMPTS\tNOT_BEFORE\tLEASE_EXPIRES_AT\tHEARTBEAT_AT\tCREATED_AT\tUPDATED_AT\n")
	fmt.Fprintf(
		&b,
		"%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
		formatQueueEmpty(item.IdempotencyKey),
		item.MaxAttempts,
		formatQueueTime(item.NotBefore),
		formatQueueTime(item.LeaseExpiresAt),
		formatQueueTime(item.HeartbeatAt),
		formatQueueTime(item.CreatedAt),
		formatQueueTime(item.UpdatedAt),
	)
	b.WriteString("Payload\n")
	fmt.Fprintf(&b, "%s\n", formatQueuePayload(item.Payload))

	b.WriteString("\nBlocks\n")
	renderQueueDeps(&b, detail.Blocks)

	b.WriteString("\nBlockedBy\n")
	renderQueueDeps(&b, detail.BlockedBy)

	b.WriteString("\nResult\n")
	if detail.Result == nil {
		b.WriteString("-\n")
	} else {
		b.WriteString("STATUS\tLOG_PATH\tSTARTED_AT\tFINISHED_AT\n")
		fmt.Fprintf(
			&b,
			"%s\t%s\t%s\t%s\n",
			detail.Result.Status,
			formatQueueEmpty(detail.Result.LogPath),
			formatQueueTime(detail.Result.StartedAt),
			formatQueueTime(detail.Result.FinishedAt),
		)
		b.WriteString("Payload\n")
		fmt.Fprintf(&b, "%s\n", formatQueuePayload(detail.Result.Payload))
	}

	b.WriteString("\nLive events\n")
	count := 0
	for _, event := range events {
		if !eventMatchesQueueItem(event, item) {
			continue
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\n", event.Type, formatQueueEventActor(event), formatEventMessage(event))
		count++
	}
	if count == 0 {
		b.WriteString("-\n")
	}

	return b.String()
}

func sortedQueueItems(items []workitem.Item) []workitem.Item {
	sorted := append([]workitem.Item(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		left := sorted[i]
		right := sorted[j]
		if left.State != right.State {
			return left.State < right.State
		}
		if left.Priority != right.Priority {
			return left.Priority > right.Priority
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.ID < right.ID
	})
	return sorted
}

func renderQueueDeps(b *strings.Builder, deps []workqueue.Dep) {
	if len(deps) == 0 {
		b.WriteString("-\n")
		return
	}
	b.WriteString("ID\tKIND\tSOURCE_REF\tSTATE\n")
	for _, dep := range deps {
		fmt.Fprintf(b, "%s\t%s\t%s\t%s\n", dep.ID, dep.Kind, dep.SourceRef, dep.State)
	}
}

func formatQueueStateCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "-"
	}

	states := make([]string, 0, len(counts))
	for state := range counts {
		states = append(states, state)
	}
	sort.Strings(states)

	parts := make([]string, 0, len(states))
	for _, state := range states {
		parts = append(parts, fmt.Sprintf("%s=%d", state, counts[state]))
	}
	return strings.Join(parts, " ")
}

func formatQueueClaimedBy(claimedBy string) string {
	if strings.TrimSpace(claimedBy) == "" {
		return "-"
	}
	return claimedBy
}

func formatQueueItemAge(item workitem.Item, now time.Time) string {
	if item.CreatedAt.IsZero() {
		return "-"
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	age := now.Sub(item.CreatedAt)
	if age < 0 {
		age = 0
	}
	return age.Round(time.Second).String()
}

func formatQueueEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func formatQueueTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func formatQueuePayload(value []byte) string {
	if len(value) == 0 {
		return "-"
	}
	return string(value)
}

func eventMatchesQueueItem(event contracts.Event, item workitem.Item) bool {
	if event.ItemID != "" {
		return event.ItemID == item.ID
	}
	if event.TaskID != "" {
		return event.TaskID == item.ID || event.TaskID == item.SourceRef
	}
	return event.Source == item.Source && event.SourceRef == item.SourceRef
}

func formatQueueEventActor(event contracts.Event) string {
	for _, actor := range []string{event.RunnerID, event.WorkerID, event.Proc} {
		if strings.TrimSpace(actor) != "" {
			return actor
		}
	}
	return "-"
}
