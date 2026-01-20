package runner

import "testing"

func TestRunLoopStopsAfterMaxCompletions(t *testing.T) {
	calls := 0
	runOnce := func(opts RunOnceOptions, deps RunOnceDeps) (string, error) {
		calls++
		return "completed", nil
	}

	count, err := RunLoop(RunOnceOptions{}, RunOnceDeps{}, 2, runOnce)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}
	if calls != 2 {
		t.Fatalf("expected 2 runs, got %d", calls)
	}
}

func TestRunLoopStopsOnNoTasks(t *testing.T) {
	calls := 0
	results := []string{"completed", "no_tasks", "completed"}
	runOnce := func(opts RunOnceOptions, deps RunOnceDeps) (string, error) {
		if calls >= len(results) {
			t.Fatalf("unexpected run %d", calls+1)
		}
		result := results[calls]
		calls++
		return result, nil
	}

	count, err := RunLoop(RunOnceOptions{}, RunOnceDeps{}, 0, runOnce)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}
	if calls != 2 {
		t.Fatalf("expected 2 runs, got %d", calls)
	}
}
