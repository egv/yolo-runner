package beads

import "testing"

func TestTraceJSONParseIgnoresLeadingLogNoise(t *testing.T) {
	payload := "2026-03-18T18:29:22.246754Z ERROR fsqlite_wal::wal: WAL frame salt mismatch\n[{\"id\":\"task-1\",\"issue_type\":\"task\",\"status\":\"open\"}]\n"
	var issues []Issue

	if err := traceJSONParse("test", []byte(payload), &issues); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "task-1" {
		t.Fatalf("unexpected issues: %#v", issues)
	}
}
