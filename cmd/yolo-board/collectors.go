package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/contracts"
)

type collectorRow struct {
	name        string
	sourceType  string
	pending     int
	active      int
	done        int
	lastPollAt  time.Time
	lastError   string
	heartbeatAt time.Time
}

func renderCollectorsTab(snapshot boardSnapshot, events []contracts.Event, now time.Time, cursor int) string {
	rows := collectorRows(snapshot, events)
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(rows) && len(rows) > 0 {
		cursor = len(rows) - 1
	}

	var b strings.Builder
	b.WriteString("Collectors\n")
	b.WriteString("NAME\tTYPE\tPENDING\tACTIVE\tDONE\tLAST POLL\tLAST ERROR\tHEARTBEAT\n")
	for i, row := range rows {
		prefix := "  "
		if i == cursor {
			prefix = "> "
		}
		fmt.Fprintf(
			&b,
			"%s%s\t%s\t%d\t%d\t%d\t%s\t%s\t%s\n",
			prefix,
			row.name,
			row.sourceType,
			row.pending,
			row.active,
			row.done,
			formatAge(row.lastPollAt, now),
			formatEmpty(row.lastError),
			formatAge(row.heartbeatAt, now),
		)
	}
	return b.String()
}

func collectorRows(snapshot boardSnapshot, events []contracts.Event) []collectorRow {
	bySource := map[string]*collectorRow{}
	get := func(source string) *collectorRow {
		source = strings.TrimSpace(source)
		if source == "" {
			return nil
		}
		row := bySource[source]
		if row == nil {
			row = &collectorRow{name: source, sourceType: inferCollectorType(source)}
			bySource[source] = row
		}
		return row
	}

	for _, source := range snapshot.sources {
		row := get(source.Source)
		if row == nil {
			continue
		}
		switch normalizeCollectorState(source.State) {
		case "pending":
			row.pending += source.Count
		case "active":
			row.active += source.Count
		case "done":
			row.done += source.Count
		}
	}
	for _, item := range snapshot.items {
		row := get(item.Source)
		if row == nil {
			continue
		}
		if item.UpdatedAt.After(row.lastPollAt) {
			row.lastPollAt = item.UpdatedAt
		}
	}
	for _, event := range events {
		if !isSourcehostEvent(event) {
			continue
		}
		source := eventSourceName(event)
		row := get(source)
		if row == nil {
			continue
		}
		switch event.Type {
		case contracts.EventTypeSourcePoll:
			if event.Timestamp.After(row.lastPollAt) {
				row.lastPollAt = event.Timestamp
			}
			row.lastError = event.Metadata["last_error"]
		case contracts.EventTypeSourceHeartbeat:
			if event.Timestamp.After(row.heartbeatAt) {
				row.heartbeatAt = event.Timestamp
			}
		}
	}

	rows := make([]collectorRow, 0, len(bySource))
	for _, row := range bySource {
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].name < rows[j].name
	})
	return rows
}

func normalizeCollectorState(state string) string {
	switch strings.TrimSpace(strings.ToLower(state)) {
	case "pending", "open":
		return "pending"
	case "active", "running", "claimed":
		return "active"
	case "done", "completed", "complete":
		return "done"
	default:
		return ""
	}
}

func inferCollectorType(name string) string {
	name = strings.TrimSpace(name)
	for _, sep := range []string{"-", "_", ":", "/"} {
		if before, _, ok := strings.Cut(name, sep); ok && before != "" {
			return before
		}
	}
	if name == "" {
		return "-"
	}
	return name
}

func isSourcehostEvent(event contracts.Event) bool {
	if event.Metadata["component"] == "sourcehost" {
		return true
	}
	return strings.HasPrefix(event.Proc, "sourcehost")
}

func eventSourceName(event contracts.Event) string {
	if source := strings.TrimSpace(event.Source); source != "" {
		return source
	}
	return strings.TrimSpace(event.Metadata["source"])
}

func formatAge(timestamp time.Time, now time.Time) string {
	if timestamp.IsZero() {
		return "-"
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	age := now.Sub(timestamp)
	if age < 0 {
		age = 0
	}
	return age.Round(time.Second).String()
}

func formatEmpty(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}
