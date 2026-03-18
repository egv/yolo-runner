package contracts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFinalizeRunErrorUsesContextOutcome(t *testing.T) {
	t.Run("propagates canceled context when runner returns nil", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := FinalizeRunError(ctx, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled, got %v", err)
		}
	})

	t.Run("normalizes wrapped deadline exceeded", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		time.Sleep(time.Millisecond)

		err := FinalizeRunError(ctx, errors.New("runner failed"))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected deadline exceeded, got %v", err)
		}
	})
}

func TestBuildRunnerArtifactsIncludesCommonFieldsAndExtras(t *testing.T) {
	started := time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC)
	finished := started.Add(2 * time.Minute)

	artifacts := BuildRunnerArtifacts("codex", RunnerRequest{
		Model:    "openai/gpt-5.3-codex",
		Mode:     RunnerModeReview,
		Metadata: map[string]string{"clone_path": "/tmp/task-clone"},
	}, RunnerResult{
		Status:     RunnerResultBlocked,
		Reason:     "runner timeout after 10m0s",
		LogPath:    "/tmp/run.jsonl",
		StartedAt:  started,
		FinishedAt: finished,
	}, map[string]string{
		"review_verdict": "fail",
	})

	expected := map[string]string{
		"backend":        "codex",
		"status":         "blocked",
		"model":          "openai/gpt-5.3-codex",
		"mode":           "review",
		"log_path":       "/tmp/run.jsonl",
		"started_at":     "2026-03-18T10:00:00Z",
		"finished_at":    "2026-03-18T10:02:00Z",
		"reason":         "runner timeout after 10m0s",
		"clone_path":     "/tmp/task-clone",
		"review_verdict": "fail",
	}
	if !reflect.DeepEqual(artifacts, expected) {
		t.Fatalf("unexpected artifacts:\nwant: %#v\ngot:  %#v", expected, artifacts)
	}
}

func TestNewRunnerOutputProgressNormalizesOutput(t *testing.T) {
	progress, ok := NewRunnerOutputProgress("stderr", " warn line \x00 ", time.Date(2026, 3, 18, 11, 0, 0, 0, time.UTC))
	if !ok {
		t.Fatalf("expected progress event")
	}
	if progress.Type != "runner_output" {
		t.Fatalf("expected runner_output type, got %q", progress.Type)
	}
	if progress.Message != "stderr: warn line" {
		t.Fatalf("unexpected message %q", progress.Message)
	}
	if progress.Timestamp.Format(time.RFC3339) != "2026-03-18T11:00:00Z" {
		t.Fatalf("unexpected timestamp %s", progress.Timestamp.Format(time.RFC3339))
	}
	if progress.Metadata["source"] != "stderr" {
		t.Fatalf("expected stderr source metadata, got %#v", progress.Metadata)
	}

	if _, ok := NewRunnerOutputProgress("stdout", " \n ", time.Now().UTC()); ok {
		t.Fatalf("expected empty line to be ignored")
	}
}

func TestBackendLogSidecarPathSupportsStderrAndProtocolTrace(t *testing.T) {
	logPath := "/tmp/runner/task-1.jsonl"
	if got := BackendLogSidecarPath(logPath, BackendLogStderr); got != "/tmp/runner/task-1.stderr.log" {
		t.Fatalf("unexpected stderr path %q", got)
	}
	if got := BackendLogSidecarPath(logPath, BackendLogProtocolTrace); got != "/tmp/runner/task-1.protocol.log" {
		t.Fatalf("unexpected protocol path %q", got)
	}
}

func TestBackendLogSidecarPathReturnsEmptyForUnsetLogPath(t *testing.T) {
	if got := BackendLogSidecarPath("", BackendLogStderr); got != "" {
		t.Fatalf("expected empty stderr sidecar path, got %q", got)
	}
	if got := BackendLogSidecarPath(" \t ", BackendLogProtocolTrace); got != "" {
		t.Fatalf("expected empty protocol sidecar path, got %q", got)
	}
}

func TestReadinessChecksSupportHTTPAndStdio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Ready") != "true" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := CheckHTTPReadiness(context.Background(), server.Client(), HTTPReadinessCheck{
		Endpoint: server.URL,
		Method:   http.MethodGet,
		Headers:  map[string]string{"X-Ready": "true"},
	}); err != nil {
		t.Fatalf("expected healthy endpoint, got %v", err)
	}

	runCalls := 0
	err := CheckStdioReadiness(context.Background(), StdioReadinessCheck{
		Command: "agent --health",
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			runCalls++
			if name != "agent" || strings.Join(args, " ") != "--health" {
				t.Fatalf("unexpected command invocation %q %q", name, args)
			}
			return []byte("ok"), nil
		},
	})
	if err != nil {
		t.Fatalf("expected healthy stdio command, got %v", err)
	}
	if runCalls != 1 {
		t.Fatalf("expected one stdio invocation, got %d", runCalls)
	}
}
