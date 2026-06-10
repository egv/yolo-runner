package splitter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

var (
	strictOutputJSONKeys = []string{"epics", "tasks", "order", "risk_notes"}
	epicJSONKeys         = []string{"name", "goal"}
	dependencyJSONKeys   = []string{"from", "to"}
	taskJSONKeys         = []string{
		"id",
		"title",
		"why",
		"in_scope",
		"out_of_scope",
		"strict_tdd",
		"done_when",
		"expected_files",
		"depends_on",
		"unlocks",
	}
)

func ParseStrictJSONOutput(input string) (StrictOutput, error) {
	raw, err := normalizeStrictJSONInput(input)
	if err != nil {
		return StrictOutput{}, err
	}
	if err := validateStrictJSONShape(raw); err != nil {
		return StrictOutput{}, err
	}

	var output StrictOutput
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		return StrictOutput{}, fmt.Errorf("decode strict splitter JSON: %w", err)
	}
	if err := ensureNoTrailingJSONTokens(decoder); err != nil {
		return StrictOutput{}, err
	}
	if err := validateStrictJSONOutput(&output); err != nil {
		return StrictOutput{}, err
	}
	return output, nil
}

func normalizeStrictJSONInput(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", fmt.Errorf("strict splitter output is empty")
	}
	if strings.HasPrefix(trimmed, "```") {
		return "", fmt.Errorf("expected raw JSON object, got markdown code fence")
	}
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return "", fmt.Errorf("expected raw JSON object")
	}
	return trimmed, nil
}

func validateStrictJSONShape(raw string) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		return fmt.Errorf("invalid strict splitter JSON: %w", err)
	}
	if err := requireExactJSONKeys("splitter output", top, strictOutputJSONKeys); err != nil {
		return err
	}

	var epics []map[string]json.RawMessage
	if isJSONNull(top["epics"]) {
		return fmt.Errorf("field epics must be an array of objects")
	}
	if err := json.Unmarshal(top["epics"], &epics); err != nil {
		return fmt.Errorf("field epics must be an array of objects: %w", err)
	}
	for i, epic := range epics {
		if err := requireExactJSONKeys(fmt.Sprintf("epics[%d]", i), epic, epicJSONKeys); err != nil {
			return err
		}
	}

	var tasks []map[string]json.RawMessage
	if isJSONNull(top["tasks"]) {
		return fmt.Errorf("field tasks must be an array of objects")
	}
	if err := json.Unmarshal(top["tasks"], &tasks); err != nil {
		return fmt.Errorf("field tasks must be an array of objects: %w", err)
	}
	for i, task := range tasks {
		if err := requireExactJSONKeys(fmt.Sprintf("tasks[%d]", i), task, taskJSONKeys); err != nil {
			return err
		}
	}

	var order []map[string]json.RawMessage
	if isJSONNull(top["order"]) {
		return fmt.Errorf("field order must be an array of objects")
	}
	if err := json.Unmarshal(top["order"], &order); err != nil {
		return fmt.Errorf("field order must be an array of objects: %w", err)
	}
	for i, dependency := range order {
		if err := requireExactJSONKeys(fmt.Sprintf("order[%d]", i), dependency, dependencyJSONKeys); err != nil {
			return err
		}
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func requireExactJSONKeys(objectName string, raw map[string]json.RawMessage, allowedKeys []string) error {
	if raw == nil {
		return fmt.Errorf("%s must be a JSON object", objectName)
	}
	allowed := make(map[string]bool, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = true
		if _, ok := raw[key]; !ok {
			return fmt.Errorf("%s missing required JSON field %q", objectName, key)
		}
	}
	for key := range raw {
		if !allowed[key] {
			return fmt.Errorf("%s contains unexpected JSON field %q", objectName, key)
		}
	}
	return nil
}

func ensureNoTrailingJSONTokens(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode strict splitter JSON trailing data: %w", err)
	}
	if len(bytes.TrimSpace(extra)) > 0 {
		return fmt.Errorf("strict splitter JSON contains trailing data")
	}
	return nil
}

func validateStrictJSONOutput(output *StrictOutput) error {
	if len(output.Epics) == 0 {
		return fmt.Errorf("field epics must contain at least one epic")
	}
	for i := range output.Epics {
		epic := &output.Epics[i]
		epic.Name = strings.TrimSpace(epic.Name)
		epic.Goal = strings.TrimSpace(epic.Goal)
		if epic.Name == "" {
			return fmt.Errorf("epics[%d].name is required", i)
		}
		if epic.Goal == "" {
			return fmt.Errorf("epics[%d].goal is required", i)
		}
	}

	if len(output.Tasks) == 0 {
		return fmt.Errorf("field tasks must contain at least one task")
	}
	taskIDs := make(map[string]bool, len(output.Tasks))
	for i := range output.Tasks {
		task := &output.Tasks[i]
		task.ID = trimRef(task.ID)
		task.Title = strings.TrimSpace(task.Title)
		if task.ID == "" {
			return fmt.Errorf("tasks[%d].id is required", i)
		}
		if !simpleRefRE.MatchString(task.ID) {
			return fmt.Errorf("tasks[%d].id %q is invalid", i, task.ID)
		}
		if taskIDs[task.ID] {
			return fmt.Errorf("duplicate task id %q", task.ID)
		}
		if task.Title == "" {
			return fmt.Errorf("task %s field title is required", task.ID)
		}
		taskIDs[task.ID] = true
	}

	for i := range output.Tasks {
		task := &output.Tasks[i]
		var err error
		if task.Why, err = validateRequiredStringList(task.ID, "why", task.Why); err != nil {
			return err
		}
		if task.InScope, err = validateRequiredStringList(task.ID, "in_scope", task.InScope); err != nil {
			return err
		}
		if task.OutOfScope, err = validateRequiredStringList(task.ID, "out_of_scope", task.OutOfScope); err != nil {
			return err
		}
		if task.StrictTDD, err = validateRequiredStringList(task.ID, "strict_tdd", task.StrictTDD); err != nil {
			return err
		}
		if task.DoneWhen, err = validateRequiredStringList(task.ID, "done_when", task.DoneWhen); err != nil {
			return err
		}
		if task.ExpectedFiles, err = validateRequiredStringList(task.ID, "expected_files", task.ExpectedFiles); err != nil {
			return err
		}
		if task.DependsOn, err = validateReferenceList(task.ID, "depends_on", task.DependsOn, taskIDs); err != nil {
			return err
		}
		if task.Unlocks, err = validateReferenceList(task.ID, "unlocks", task.Unlocks, taskIDs); err != nil {
			return err
		}
	}

	if err := validateOrderReferences(output.Order, taskIDs); err != nil {
		return err
	}

	riskNotes, err := validateRiskNotes(output.RiskNotes)
	if err != nil {
		return err
	}
	output.RiskNotes = riskNotes
	return nil
}

func validateRequiredStringList(taskID, field string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("task %s field %s must contain at least one item", taskID, field)
	}
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item == "" {
			return nil, fmt.Errorf("task %s field %s contains an empty item", taskID, field)
		}
		if isNone(item) {
			return nil, fmt.Errorf("task %s field %s must contain concrete text, not none", taskID, field)
		}
		cleaned = append(cleaned, item)
	}
	return cleaned, nil
}

func validateReferenceList(taskID, field string, values []string, taskIDs map[string]bool) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("task %s field %s must contain at least one item or none", taskID, field)
	}
	refs := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		ref := trimRef(value)
		if ref == "" {
			return nil, fmt.Errorf("task %s field %s contains an empty dependency reference", taskID, field)
		}
		if isNone(ref) {
			continue
		}
		if ambiguousWordRE.MatchString(ref) || strings.ContainsAny(ref, "/&,") {
			return nil, fmt.Errorf("task %s field %s contains ambiguous dependency reference %q", taskID, field, value)
		}
		if !simpleRefRE.MatchString(ref) {
			return nil, fmt.Errorf("task %s field %s contains invalid dependency reference %q", taskID, field, ref)
		}
		if !taskIDs[ref] {
			return nil, fmt.Errorf("task %s field %s references unknown task %q", taskID, field, ref)
		}
		if ref == taskID {
			return nil, fmt.Errorf("task %s field %s must not reference itself", taskID, field)
		}
		if !seen[ref] {
			refs = append(refs, ref)
			seen[ref] = true
		}
	}
	return refs, nil
}

func validateOrderReferences(order []Dependency, taskIDs map[string]bool) error {
	seen := make(map[string]bool, len(order))
	for i := range order {
		dependency := &order[i]
		dependency.From = trimRef(dependency.From)
		dependency.To = trimRef(dependency.To)
		if dependency.From == "" {
			return fmt.Errorf("order[%d].from is required", i)
		}
		if dependency.To == "" {
			return fmt.Errorf("order[%d].to is required", i)
		}
		if !simpleRefRE.MatchString(dependency.From) {
			return fmt.Errorf("order[%d].from %q is invalid", i, dependency.From)
		}
		if !simpleRefRE.MatchString(dependency.To) {
			return fmt.Errorf("order[%d].to %q is invalid", i, dependency.To)
		}
		if !taskIDs[dependency.From] {
			return fmt.Errorf("order[%d].from references unknown task %q", i, dependency.From)
		}
		if !taskIDs[dependency.To] {
			return fmt.Errorf("order[%d].to references unknown task %q", i, dependency.To)
		}
		if dependency.From == dependency.To {
			return fmt.Errorf("order[%d] must not point a task at itself", i)
		}
		key := dependency.From + "\x00" + dependency.To
		if seen[key] {
			return fmt.Errorf("duplicate order dependency %s -> %s", dependency.From, dependency.To)
		}
		seen[key] = true
	}
	return nil
}

func validateRiskNotes(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("field risk_notes must contain at least one item or none")
	}
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item == "" {
			return nil, fmt.Errorf("field risk_notes contains an empty item")
		}
		if !isNone(item) {
			cleaned = append(cleaned, item)
		}
	}
	return cleaned, nil
}
