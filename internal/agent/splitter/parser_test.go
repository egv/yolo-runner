package splitter

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseStrictOutputParsesSectionsAndRejectsInvalidOutput(t *testing.T) {
	valid := strictOutputFixture(
		strictTaskFixture("T20", "Invoke strict splitter", "Call the strict splitter prompt.", []string{"T19"}, []string{"T21"}),
		strictTaskFixture("T21", "Parse strict splitter output", "Generated Tracker subtasks need structured task sections, not prose blobs.", []string{"T20"}, []string{"T22"}),
		strictTaskFixture("T22", "Create Tracker subtasks", "Tracker needs concrete child tasks from parsed splitter output.", []string{"T21"}, []string{"none"}),
	)

	got, err := ParseStrictOutput(valid)
	if err != nil {
		t.Fatalf("ParseStrictOutput returned error: %v", err)
	}

	wantEpics := []Epic{{Name: "Tracker task generation", Goal: "Generate strict Tracker subtasks from a broad task."}}
	if !reflect.DeepEqual(got.Epics, wantEpics) {
		t.Fatalf("Epics = %#v, want %#v", got.Epics, wantEpics)
	}

	wantOrder := []Dependency{
		{From: "T20", To: "T21"},
		{From: "T21", To: "T22"},
	}
	if !reflect.DeepEqual(got.Order, wantOrder) {
		t.Fatalf("Order = %#v, want %#v", got.Order, wantOrder)
	}

	wantRiskNotes := []string{"Model output may include stray prose before the first heading."}
	if !reflect.DeepEqual(got.RiskNotes, wantRiskNotes) {
		t.Fatalf("RiskNotes = %#v, want %#v", got.RiskNotes, wantRiskNotes)
	}

	task := got.TaskByID("T21")
	if task == nil {
		t.Fatalf("expected T21 task, got tasks %#v", got.Tasks)
	}
	if task.Title != "Parse strict splitter output" {
		t.Fatalf("T21 title = %q", task.Title)
	}
	if !reflect.DeepEqual(task.Why, []string{"Generated Tracker subtasks need structured task sections, not prose blobs."}) {
		t.Fatalf("T21 Why = %#v", task.Why)
	}
	if !reflect.DeepEqual(task.InScope, []string{
		"Parse Epics, Tasks, Order, Risk notes, and each strict task template from splitter output.",
		"Seam: split output parser",
	}) {
		t.Fatalf("T21 InScope = %#v", task.InScope)
	}
	if !reflect.DeepEqual(task.OutOfScope, []string{"Codex invocation and Tracker subtask creation."}) {
		t.Fatalf("T21 OutOfScope = %#v", task.OutOfScope)
	}
	if !reflect.DeepEqual(task.StrictTDD, []string{
		"Add or update one targeted failing test first",
		"Run the targeted test and confirm it fails for the intended reason",
		"Implement the minimum production change needed to make it pass",
		"Re-run the targeted test",
		"Run one narrow follow-up verification command",
	}) {
		t.Fatalf("T21 StrictTDD = %#v", task.StrictTDD)
	}
	if !reflect.DeepEqual(task.DoneWhen, []string{
		"Tests reject missing strict sections and dependency-ambiguous output.",
		"The narrow verification command for the touched package passes.",
	}) {
		t.Fatalf("T21 DoneWhen = %#v", task.DoneWhen)
	}
	if !reflect.DeepEqual(task.ExpectedFiles, []string{
		"internal/agent/splitter/parser.go",
		"internal/agent/splitter/parser_test.go",
	}) {
		t.Fatalf("T21 ExpectedFiles = %#v", task.ExpectedFiles)
	}
	if !reflect.DeepEqual(task.DependsOn, []string{"T20"}) {
		t.Fatalf("T21 DependsOn = %#v", task.DependsOn)
	}
	if !reflect.DeepEqual(task.Unlocks, []string{"T22"}) {
		t.Fatalf("T21 Unlocks = %#v", task.Unlocks)
	}

	missingOutOfScope := strings.Replace(valid, "Out of scope:\n- Codex invocation and Tracker subtask creation.\n\nStrict TDD:", "Strict TDD:", 1)
	_, err = ParseStrictOutput(missingOutOfScope)
	if err == nil {
		t.Fatal("expected missing strict section error")
	}
	if !strings.Contains(err.Error(), "T21") || !strings.Contains(err.Error(), "Out of scope") {
		t.Fatalf("missing strict section error = %q", err.Error())
	}

	ambiguousOrder := strings.Replace(valid, "- T20 -> T21 -> T22", "- T20 or T21 -> T22", 1)
	_, err = ParseStrictOutput(ambiguousOrder)
	if err == nil {
		t.Fatal("expected ambiguous dependency error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "ambiguous") || !strings.Contains(err.Error(), "Order") {
		t.Fatalf("ambiguous dependency error = %q", err.Error())
	}
}

func TestParseStrictOutputAcceptsTaskTemplatesInsideTasksSection(t *testing.T) {
	output := strings.Join([]string{
		"## Epics",
		"- Messenger platform parity: Add a parallel Yandex Messenger bot.",
		"",
		"## Tasks",
		strictTaskFixture("ADAPTABOT-1.1", "Extract shared inbound message model", "A shared inbound message model creates the first seam.", []string{"none"}, []string{"ADAPTABOT-1.2"}),
		strictTaskFixture("ADAPTABOT-1.2", "Extract shared outbound response model", "A shared outbound response model separates bot behavior from platform send APIs.", []string{"ADAPTABOT-1.1"}, []string{"none"}),
		"## Order",
		"- ADAPTABOT-1.1 -> ADAPTABOT-1.2",
		"",
		"## Risk notes",
		"- Repository structure is unknown because no files were inspected.",
	}, "\n")

	got, err := ParseStrictOutput(output)
	if err != nil {
		t.Fatalf("ParseStrictOutput returned error: %v", err)
	}
	if len(got.Tasks) != 2 {
		t.Fatalf("expected two tasks, got %#v", got.Tasks)
	}
	if got.Tasks[0].ID != "ADAPTABOT-1.1" || got.Tasks[0].Title != "Extract shared inbound message model" {
		t.Fatalf("unexpected first task: %#v", got.Tasks[0])
	}
	if !reflect.DeepEqual(got.Tasks[1].DependsOn, []string{"ADAPTABOT-1.1"}) {
		t.Fatalf("second task DependsOn = %#v", got.Tasks[1].DependsOn)
	}
}

func strictOutputFixture(tasks ...string) string {
	sections := []string{
		"Introductory prose should be ignored.",
		"",
		"## Epics",
		"- Tracker task generation: Generate strict Tracker subtasks from a broad task.",
		"",
		"## Tasks",
		"- T20: Invoke strict splitter",
		"- T21: Parse strict splitter output",
		"- T22: Create Tracker subtasks",
		"",
		"## Order",
		"- T20 -> T21 -> T22",
		"",
		"## Risk notes",
		"- Model output may include stray prose before the first heading.",
		"",
	}
	sections = append(sections, tasks...)
	return strings.Join(sections, "\n")
}

func strictTaskFixture(id, title, why string, dependsOn, unlocks []string) string {
	if id == "T21" {
		return strings.Join([]string{
			"### Task: T21 Parse strict splitter output",
			"",
			"Why:",
			"- " + why,
			"",
			"In scope:",
			"- Parse Epics, Tasks, Order, Risk notes, and each strict task template from splitter output.",
			"- Seam: split output parser",
			"",
			"Out of scope:",
			"- Codex invocation and Tracker subtask creation.",
			"",
			"Strict TDD:",
			"1. Add or update one targeted failing test first",
			"2. Run the targeted test and confirm it fails for the intended reason",
			"3. Implement the minimum production change needed to make it pass",
			"4. Re-run the targeted test",
			"5. Run one narrow follow-up verification command",
			"",
			"Done when:",
			"- Tests reject missing strict sections and dependency-ambiguous output.",
			"- The narrow verification command for the touched package passes.",
			"",
			"Expected files:",
			"- internal/agent/splitter/parser.go",
			"- internal/agent/splitter/parser_test.go",
			"",
			"Depends on:",
			"- " + strings.Join(dependsOn, ", "),
			"",
			"Unlocks:",
			"- " + strings.Join(unlocks, ", "),
			"",
		}, "\n")
	}

	return strings.Join([]string{
		"### Task: " + id + " " + title,
		"",
		"Why:",
		"- " + why,
		"",
		"In scope:",
		"- Implement one focused behavior.",
		"",
		"Out of scope:",
		"- Other task slices.",
		"",
		"Strict TDD:",
		"1. Add or update one targeted failing test first",
		"2. Run the targeted test and confirm it fails for the intended reason",
		"3. Implement the minimum production change needed to make it pass",
		"4. Re-run the targeted test",
		"5. Run one narrow follow-up verification command",
		"",
		"Done when:",
		"- The targeted test passes.",
		"",
		"Expected files:",
		"- internal/agent/splitter/parser.go",
		"",
		"Depends on:",
		"- " + strings.Join(dependsOn, ", "),
		"",
		"Unlocks:",
		"- " + strings.Join(unlocks, ", "),
		"",
	}, "\n")
}
