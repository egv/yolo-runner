package arcanum

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

// ParsePRChecksJSON parses checks embedded in JSON emitted by: arc pr status --json <pr>.
//
// The arc CLI on the dev host exposes no separate `arc pr checks` mode; checks
// are expected as a list field on the status payload when available.
func ParsePRChecksJSON(data []byte) ([]arcreview.PRCheck, error) {
	rawChecks, err := prCheckItems(data)
	if err != nil {
		return nil, err
	}

	checks := make([]arcreview.PRCheck, 0, len(rawChecks))
	for i, raw := range rawChecks {
		check, err := parseArcPRCheck(raw)
		if err != nil {
			return nil, fmt.Errorf("parse arc PR check %d: %w", i, err)
		}
		if check.Name != "" || check.Status != "" {
			checks = append(checks, check)
		}
	}
	return checks, nil
}

func prCheckItems(data []byte) ([]json.RawMessage, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err == nil {
		return list, nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, fmt.Errorf("parse arc PR checks JSON: %w", err)
	}

	for _, key := range []string{
		"checks",
		"check_results",
		"checkResults",
		"status_checks",
		"statusChecks",
		"statuses",
		"data",
		"items",
		"result",
	} {
		raw := object[key]
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		items, ok, err := rawPRCheckList(raw)
		if err != nil {
			return nil, fmt.Errorf("parse %q as PR checks: %w", key, err)
		}
		if ok {
			return items, nil
		}
	}

	return nil, nil
}

func rawPRCheckList(raw json.RawMessage) ([]json.RawMessage, bool, error) {
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, true, nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, false, nil
	}

	for _, key := range []string{"checks", "items", "results", "data"} {
		nested := object[key]
		if len(nested) == 0 || string(nested) == "null" {
			continue
		}
		if err := json.Unmarshal(nested, &list); err != nil {
			return nil, false, err
		}
		return list, true, nil
	}
	return nil, false, nil
}

func parseArcPRCheck(raw json.RawMessage) (arcreview.PRCheck, error) {
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return arcreview.PRCheck{}, err
	}

	startedAt, err := timeScalar(item, "started_at", "startedAt", "start_time", "startTime")
	if err != nil {
		return arcreview.PRCheck{}, err
	}
	completedAt, err := timeScalar(item, "completed_at", "completedAt", "finished_at", "finishedAt", "finish_time", "finishTime")
	if err != nil {
		return arcreview.PRCheck{}, err
	}

	return arcreview.PRCheck{
		Name:        prCheckName(item),
		Status:      normalizeArcPRCheckStatus(prCheckStatus(item)),
		Summary:     firstScalar(item, "summary", "description", "message", "details", "text"),
		URL:         firstScalar(item, "url", "target_url", "targetUrl", "details_url", "detailsUrl", "web_url", "webUrl"),
		StartedAt:   startedAt,
		CompletedAt: completedAt,
	}, nil
}

func prCheckName(item map[string]json.RawMessage) string {
	if name := firstScalar(item, "name", "title", "check", "check_name", "checkName", "context", "id"); name != "" {
		return name
	}
	if check := rawObject(item["check"]); check != nil {
		return firstScalar(check, "name", "title", "context", "id")
	}
	return ""
}

func prCheckStatus(item map[string]json.RawMessage) string {
	for _, key := range []string{"status", "state", "conclusion", "result", "verdict"} {
		if status := prCheckStatusValue(item[key]); status != "" {
			return status
		}
	}
	if check := rawObject(item["check"]); check != nil {
		return prCheckStatus(check)
	}
	return ""
}

func prCheckStatusValue(raw json.RawMessage) string {
	if status := scalarValue(raw); status != "" {
		return status
	}
	object := rawObject(raw)
	if object == nil {
		return ""
	}
	return firstScalar(object, "status", "state", "name", "value", "code")
}

func normalizeArcPRCheckStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	normalized = strings.NewReplacer(" ", "_", "/", "_").Replace(normalized)

	switch normalized {
	case "bad", "fail":
		return "failed"
	case "good", "okay":
		return "success"
	case "in-progress":
		return "in_progress"
	default:
		return normalized
	}
}
