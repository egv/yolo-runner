package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/workitem"
)

func renderQueueTab(snapshot boardSnapshot, now time.Time) string {
	var b strings.Builder
	b.WriteString("Queue\n")
	fmt.Fprintf(&b, "Counts: %s\n", formatQueueStateCounts(snapshot.stateCounts))
	b.WriteString("KIND\tSOURCE_REF\tPRESET\tPRIORITY\tSTATE\tATTEMPT\tCLAIMED_BY\tAGE\n")

	items := append([]workitem.Item(nil), snapshot.items...)
	sort.SliceStable(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
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
