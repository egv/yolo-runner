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

func TestRunLoopUpdatesProgressCounter(t *testing.T) {
	runs := 0
	capture := []ProgressState{}
	results := []string{"completed", "blocked", "completed", "no_tasks"}
	runOnce := func(opts RunOnceOptions, deps RunOnceDeps) (string, error) {
		capture = append(capture, opts.Progress)
		result := results[runs]
		runs++
		return result, nil
	}

	count, err := RunLoop(RunOnceOptions{Progress: ProgressState{Total: 3}}, RunOnceDeps{}, 0, runOnce)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected count 3, got %d", count)
	}
	if len(capture) != 4 {
		t.Fatalf("expected 4 runs, got %d", len(capture))
	}
	if capture[0].Completed != 0 || capture[0].Total != 3 {
		t.Fatalf("expected initial progress [0/3], got %#v", capture[0])
	}
	if capture[1].Completed != 1 || capture[1].Total != 3 {
		t.Fatalf("expected progress [1/3], got %#v", capture[1])
	}
	if capture[2].Completed != 2 || capture[2].Total != 3 {
		t.Fatalf("expected progress [2/3], got %#v", capture[2])
	}
	if capture[3].Completed != 3 || capture[3].Total != 3 {
		t.Fatalf("expected progress [3/3], got %#v", capture[3])
	}
}
