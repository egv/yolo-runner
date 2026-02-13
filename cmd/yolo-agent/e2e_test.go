package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anomalyco/yolo-runner/internal/claude"
	"github.com/anomalyco/yolo-runner/internal/codex"
	"github.com/anomalyco/yolo-runner/internal/contracts"
	githubtracker "github.com/anomalyco/yolo-runner/internal/github"
	"github.com/anomalyco/yolo-runner/internal/kimi"
	"github.com/anomalyco/yolo-runner/internal/linear"
	"github.com/anomalyco/yolo-runner/internal/tk"
	"github.com/anomalyco/yolo-runner/internal/ui/monitor"
)

func TestE2E_YoloAgentRunCompletesSeededTKTask(t *testing.T) {
	if _, err := exec.LookPath("tk"); err != nil {
		t.Skip("tk CLI is required for e2e test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI is required for e2e test")
	}

	repo := t.TempDir()
	runCommand(t, repo, "git", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runCommand(t, repo, "git", "add", "README.md")
	runCommand(t, repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	runner := localRunner{dir: repo}
	rootID := mustCreateTicket(t, runner, "Roadmap", "epic", "0", "")
	taskID := mustCreateTicket(t, runner, "Self-host task", "task", "0", rootID)

	taskManager := tk.NewTaskManager(runner)
	fakeAgent := &fakeAgentRunner{results: []contracts.RunnerResult{{Status: contracts.RunnerResultCompleted}, {Status: contracts.RunnerResultCompleted, ReviewReady: true}}}
	fakeVCS := &fakeVCS{}

	err := runWithComponents(context.Background(), runConfig{repoRoot: repo, rootID: rootID, maxTasks: 1}, taskManager, fakeAgent, fakeVCS)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	task, err := taskManager.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}
	if task.Status != contracts.TaskStatusClosed {
		t.Fatalf("expected task to be closed, got %s", task.Status)
	}
	if len(fakeAgent.requests) == 0 {
		t.Fatalf("expected runner to be invoked")
	}
	if fakeAgent.requests[0].RepoRoot == repo {
		t.Fatalf("expected runner repo root to use isolated clone path, got %q", fakeAgent.requests[0].RepoRoot)
	}
}

func TestE2E_StreamSmoke_ConcurrencyEmitsMultiWorkerParseableEvents(t *testing.T) {
	if _, err := exec.LookPath("tk"); err != nil {
		t.Skip("tk CLI is required for e2e test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI is required for e2e test")
	}

	repo := t.TempDir()
	runCommand(t, repo, "git", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runCommand(t, repo, "git", "add", "README.md")
	runCommand(t, repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	runner := localRunner{dir: repo}
	rootID := mustCreateTicket(t, runner, "Roadmap", "epic", "0", "")
	mustCreateTicket(t, runner, "Task 1", "task", "0", rootID)
	mustCreateTicket(t, runner, "Task 2", "task", "0", rootID)

	taskManager := tk.NewTaskManager(runner)
	fakeAgent := &parallelFakeAgentRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}, delay: 30 * time.Millisecond}
	fakeVCS := &fakeVCS{}

	originalStdout := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writePipe
	defer func() {
		os.Stdout = originalStdout
	}()

	runErr := runWithComponents(context.Background(), runConfig{repoRoot: repo, rootID: rootID, maxTasks: 2, concurrency: 2, stream: true}, taskManager, fakeAgent, fakeVCS)
	if runErr != nil {
		t.Fatalf("run failed: %v", runErr)
	}
	if err := writePipe.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	raw, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if err := readPipe.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	decoder := contracts.NewEventDecoder(bytes.NewReader(raw))
	model := monitor.NewModel(nil)
	workers := map[string]struct{}{}
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to decode NDJSON stream: %v", err)
		}
		model.Apply(event)
		if event.Type == contracts.EventTypeRunnerStarted && strings.TrimSpace(event.WorkerID) != "" {
			workers[event.WorkerID] = struct{}{}
		}
	}
	if len(workers) < 2 {
		t.Fatalf("expected at least 2 workers in stream, got %d from %q", len(workers), string(raw))
	}
	view := model.View()
	if !strings.Contains(view, "Workers:") {
		t.Fatalf("expected model view to render workers section, got %q", view)
	}
}

func TestE2E_CodexTKConcurrency2LandsViaMergeQueue(t *testing.T) {
	if _, err := exec.LookPath("tk"); err != nil {
		t.Skip("tk CLI is required for e2e test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI is required for e2e test")
	}

	repo := t.TempDir()
	runCommand(t, repo, "git", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runCommand(t, repo, "git", "add", "README.md")
	runCommand(t, repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	runner := localRunner{dir: repo}
	rootID := mustCreateTicket(t, runner, "Roadmap", "epic", "0", "")
	taskOneID := mustCreateTicket(t, runner, "Task 1", "task", "0", rootID)
	taskTwoID := mustCreateTicket(t, runner, "Task 2", "task", "1", rootID)

	taskManager := tk.NewTaskManager(runner)
	fakeCodex := codex.NewCLIRunnerAdapter(writeFakeCodexBinary(t), nil)
	fakeVCS := &fakeVCS{}

	originalStdout := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writePipe
	defer func() {
		os.Stdout = originalStdout
	}()

	runErr := runWithComponents(context.Background(), runConfig{
		repoRoot:    repo,
		rootID:      rootID,
		backend:     backendCodex,
		model:       "openai/gpt-5.3-codex",
		maxTasks:    2,
		concurrency: 2,
		stream:      true,
	}, taskManager, fakeCodex, fakeVCS)
	if runErr != nil {
		t.Fatalf("run failed: %v", runErr)
	}
	if err := writePipe.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	raw, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if err := readPipe.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	decoder := contracts.NewEventDecoder(bytes.NewReader(raw))
	mergeLandedCount := 0
	sawRunStarted := false
	sawCodexBackend := false
	sawConcurrencyTwo := false
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to decode NDJSON stream: %v", err)
		}
		if event.Type == contracts.EventTypeRunStarted {
			sawRunStarted = true
			if event.Metadata["backend"] == backendCodex {
				sawCodexBackend = true
			}
			if event.Metadata["concurrency"] == "2" {
				sawConcurrencyTwo = true
			}
		}
		if event.Type == contracts.EventTypeMergeLanded {
			mergeLandedCount++
		}
	}
	if !sawRunStarted {
		t.Fatalf("expected run_started event in stream, got %q", string(raw))
	}
	if !sawCodexBackend {
		t.Fatalf("expected run_started metadata backend=%q, got %q", backendCodex, string(raw))
	}
	if !sawConcurrencyTwo {
		t.Fatalf("expected run_started metadata concurrency=2, got %q", string(raw))
	}
	if mergeLandedCount < 1 {
		t.Fatalf("expected at least one merge_landed event, got %d from %q", mergeLandedCount, string(raw))
	}

	for _, taskID := range []string{taskOneID, taskTwoID} {
		task, err := taskManager.GetTask(context.Background(), taskID)
		if err != nil {
			t.Fatalf("get task failed for %s: %v", taskID, err)
		}
		if task.Status != contracts.TaskStatusClosed {
			t.Fatalf("expected task %s to be closed, got %s", taskID, task.Status)
		}
	}
}

func TestE2E_LinearProfileProcessesAndClosesIssue(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI is required for e2e test")
	}

	repo := t.TempDir()
	runCommand(t, repo, "git", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runCommand(t, repo, "git", "add", "README.md")
	runCommand(t, repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	const (
		rootID      = "proj-linear-e2e"
		issueID     = "iss-linear-e2e"
		profileName = "linear-demo"
	)

	writeTrackerConfigYAML(t, repo, `
default_profile: linear-demo
profiles:
  linear-demo:
    tracker:
      type: linear
      linear:
        scope:
          workspace: acme
        auth:
          token_env: LINEAR_TOKEN
`)
	t.Setenv("LINEAR_TOKEN", "lin_api_test")

	type issueState struct {
		Type string
		Name string
	}

	var (
		stateMu           sync.Mutex
		currentState      = issueState{Type: "backlog", Name: "Backlog"}
		statusTransitions []contracts.TaskStatus
	)

	issuePayload := func(state issueState) map[string]any {
		return map[string]any{
			"id":          issueID,
			"title":       "Implement Linear e2e demo",
			"description": "End-to-end processing",
			"priority":    1,
			"project":     map[string]any{"id": rootID},
			"parent":      nil,
			"state":       map[string]any{"type": state.Type, "name": state.Name},
			"relations":   map[string]any{"nodes": []any{}},
		}
	}

	writeResponse := func(t *testing.T, w http.ResponseWriter, data any) {
		t.Helper()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
			t.Fatalf("encode GraphQL response: %v", err)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Helper()
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "lin_api_test" {
			t.Fatalf("expected Authorization=lin_api_test, got %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var payload struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode GraphQL request body: %v", err)
		}
		query := payload.Query

		switch {
		case strings.Contains(query, "AuthProbe"):
			writeResponse(t, w, map[string]any{
				"viewer": map[string]any{"id": "usr-1"},
			})
		case strings.Contains(query, "ReadProjectBacklog"):
			if !strings.Contains(query, `project(id: "proj-linear-e2e")`) {
				t.Fatalf("expected project backlog query for %q, got %q", rootID, query)
			}
			stateMu.Lock()
			state := currentState
			stateMu.Unlock()
			writeResponse(t, w, map[string]any{
				"project": map[string]any{
					"id":   rootID,
					"name": "Linear e2e project",
					"issues": map[string]any{
						"nodes": []any{issuePayload(state)},
					},
				},
			})
		case strings.Contains(query, "ReadIssueWorkflowStatesForWrite"):
			writeResponse(t, w, map[string]any{
				"issue": map[string]any{
					"id": issueID,
					"team": map[string]any{
						"states": map[string]any{
							"nodes": []any{
								map[string]any{"id": "st-open", "type": "backlog", "name": "Backlog"},
								map[string]any{"id": "st-started", "type": "started", "name": "In Progress"},
								map[string]any{"id": "st-closed", "type": "completed", "name": "Done"},
							},
						},
					},
				},
			})
		case strings.Contains(query, "UpdateIssueWorkflowState"):
			stateMu.Lock()
			switch {
			case strings.Contains(query, `stateId: "st-started"`):
				currentState = issueState{Type: "started", Name: "In Progress"}
				statusTransitions = append(statusTransitions, contracts.TaskStatusInProgress)
			case strings.Contains(query, `stateId: "st-closed"`):
				currentState = issueState{Type: "completed", Name: "Done"}
				statusTransitions = append(statusTransitions, contracts.TaskStatusClosed)
			default:
				stateMu.Unlock()
				t.Fatalf("unexpected workflow-state mutation query: %q", query)
			}
			stateMu.Unlock()
			writeResponse(t, w, map[string]any{
				"issueUpdate": map[string]any{"success": true},
			})
		case strings.Contains(query, "ReadIssue"):
			if !strings.Contains(query, `issue(id: "iss-linear-e2e")`) {
				t.Fatalf("expected issue query for %q, got %q", issueID, query)
			}
			stateMu.Lock()
			state := currentState
			stateMu.Unlock()
			writeResponse(t, w, map[string]any{
				"issue": issuePayload(state),
			})
		default:
			t.Fatalf("unexpected GraphQL query: %q", query)
		}
	}))
	t.Cleanup(server.Close)

	originalFactory := newLinearTaskManager
	newLinearTaskManager = func(cfg linear.Config) (contracts.TaskManager, error) {
		if cfg.Workspace != "acme" {
			return nil, errors.New("expected workspace acme")
		}
		if cfg.Token != "lin_api_test" {
			return nil, errors.New("expected LINEAR_TOKEN from environment")
		}
		return linear.NewTaskManager(linear.Config{
			Workspace:  cfg.Workspace,
			Token:      cfg.Token,
			Endpoint:   server.URL,
			HTTPClient: server.Client(),
		})
	}
	t.Cleanup(func() {
		newLinearTaskManager = originalFactory
	})

	profile, err := resolveTrackerProfile(repo, "", rootID, os.Getenv)
	if err != nil {
		t.Fatalf("resolve tracker profile: %v", err)
	}
	if profile.Name != profileName {
		t.Fatalf("expected profile %q, got %q", profileName, profile.Name)
	}
	if profile.Tracker.Type != trackerTypeLinear {
		t.Fatalf("expected tracker type %q, got %q", trackerTypeLinear, profile.Tracker.Type)
	}

	taskManager, err := buildTaskManagerForTracker(repo, profile)
	if err != nil {
		t.Fatalf("build linear task manager from profile: %v", err)
	}
	fakeAgent := &fakeAgentRunner{results: []contracts.RunnerResult{
		{Status: contracts.RunnerResultCompleted},
		{Status: contracts.RunnerResultCompleted, ReviewReady: true},
	}}
	fakeVCS := &fakeVCS{}

	originalStdout := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writePipe
	defer func() {
		os.Stdout = originalStdout
	}()

	runErr := runWithComponents(context.Background(), runConfig{
		repoRoot:    repo,
		rootID:      rootID,
		backend:     backendCodex,
		profile:     profile.Name,
		trackerType: profile.Tracker.Type,
		model:       "openai/gpt-5.3-codex",
		maxTasks:    1,
		stream:      true,
	}, taskManager, fakeAgent, fakeVCS)
	if runErr != nil {
		t.Fatalf("run failed: %v", runErr)
	}
	if err := writePipe.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	raw, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if err := readPipe.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	events := decodeNDJSONEvents(t, raw)
	sawRunStarted := false
	sawLinearTracker := false
	sawLinearProfile := false
	for _, event := range events {
		if event.Type != contracts.EventTypeRunStarted {
			continue
		}
		sawRunStarted = true
		if event.Metadata["tracker"] == trackerTypeLinear {
			sawLinearTracker = true
		}
		if event.Metadata["profile"] == profileName {
			sawLinearProfile = true
		}
	}
	if !sawRunStarted {
		t.Fatalf("expected run_started event in stream, got %q", string(raw))
	}
	if !sawLinearTracker {
		t.Fatalf("expected run_started metadata tracker=%q, got %q", trackerTypeLinear, string(raw))
	}
	if !sawLinearProfile {
		t.Fatalf("expected run_started metadata profile=%q, got %q", profileName, string(raw))
	}

	task, err := taskManager.GetTask(context.Background(), issueID)
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}
	if task.Status != contracts.TaskStatusClosed {
		t.Fatalf("expected linear issue %q to be closed, got %q", issueID, task.Status)
	}
	if len(fakeAgent.requests) == 0 {
		t.Fatalf("expected agent runner to be invoked at least once")
	}
	if fakeAgent.requests[0].RepoRoot == repo {
		t.Fatalf("expected agent request to use isolated clone path, got %q", fakeAgent.requests[0].RepoRoot)
	}

	stateMu.Lock()
	transitions := append([]contracts.TaskStatus(nil), statusTransitions...)
	stateMu.Unlock()
	if len(transitions) != 2 {
		t.Fatalf("expected exactly two Linear status transitions, got %#v", transitions)
	}
	if transitions[0] != contracts.TaskStatusInProgress {
		t.Fatalf("expected first transition to in_progress, got %#v", transitions)
	}
	if transitions[1] != contracts.TaskStatusClosed {
		t.Fatalf("expected second transition to closed, got %#v", transitions)
	}
}

func TestE2E_GitHubProfileProcessesAndClosesIssue(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI is required for e2e test")
	}

	repo := t.TempDir()
	runCommand(t, repo, "git", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runCommand(t, repo, "git", "add", "README.md")
	runCommand(t, repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	const (
		rootID           = "101"
		issueID          = "102"
		rootIssueNumber  = 101
		taskIssueNumber  = 102
		profileName      = "github-demo"
	)

	writeTrackerConfigYAML(t, repo, `
default_profile: github-demo
profiles:
  github-demo:
    tracker:
      type: github
      github:
        scope:
          owner: anomalyco
          repo: yolo-runner
        auth:
          token_env: GITHUB_TOKEN
`)
	t.Setenv("GITHUB_TOKEN", "ghp_test")

	var (
		stateMu           sync.Mutex
		rootState         = "open"
		issueState        = "open"
		statusTransitions []string
	)

	issuePayload := func(number int, title string, state string) map[string]any {
		return map[string]any{
			"number": number,
			"title":  title,
			"body":   "End-to-end processing",
			"state":  state,
			"labels": []map[string]string{
				{"name": "p1"},
			},
		}
	}

	writeJSON := func(t *testing.T, w http.ResponseWriter, statusCode int, data any) {
		t.Helper()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if err := json.NewEncoder(w).Encode(data); err != nil {
			t.Fatalf("encode GitHub response: %v", err)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Helper()
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "Bearer ghp_test" {
			t.Fatalf("expected Authorization=Bearer ghp_test, got %q", got)
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/anomalyco/yolo-runner":
			writeJSON(t, w, http.StatusOK, map[string]any{"full_name": "anomalyco/yolo-runner"})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/anomalyco/yolo-runner/issues":
			if got := r.URL.Query().Get("state"); got != "all" {
				t.Fatalf("expected state=all query, got %q", got)
			}
			if got := r.URL.Query().Get("per_page"); got != "100" {
				t.Fatalf("expected per_page=100 query, got %q", got)
			}
			if got := r.URL.Query().Get("page"); got != "1" {
				writeJSON(t, w, http.StatusOK, []any{})
				return
			}
			stateMu.Lock()
			root := rootState
			issue := issueState
			stateMu.Unlock()
			writeJSON(t, w, http.StatusOK, []map[string]any{
				issuePayload(rootIssueNumber, "GitHub e2e root", root),
				issuePayload(taskIssueNumber, "Implement GitHub e2e demo", issue),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/anomalyco/yolo-runner/issues/102":
			stateMu.Lock()
			issue := issueState
			stateMu.Unlock()
			writeJSON(t, w, http.StatusOK, issuePayload(taskIssueNumber, "Implement GitHub e2e demo", issue))
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/anomalyco/yolo-runner/issues/102":
			var payload struct {
				State string `json:"state"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode GitHub issue patch payload: %v", err)
			}
			stateMu.Lock()
			issueState = strings.TrimSpace(payload.State)
			statusTransitions = append(statusTransitions, issueState)
			stateMu.Unlock()
			writeJSON(t, w, http.StatusOK, map[string]any{"number": taskIssueNumber})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/anomalyco/yolo-runner/issues/102/comments":
			writeJSON(t, w, http.StatusCreated, map[string]any{"id": 1})
		default:
			t.Fatalf("unexpected GitHub API request: %s %s", r.Method, r.URL.String())
		}
	}))
	t.Cleanup(server.Close)

	originalFactory := newGitHubTaskManager
	newGitHubTaskManager = func(cfg githubtracker.Config) (contracts.TaskManager, error) {
		if cfg.Owner != "anomalyco" {
			return nil, errors.New("expected owner anomalyco")
		}
		if cfg.Repo != "yolo-runner" {
			return nil, errors.New("expected repository yolo-runner")
		}
		if cfg.Token != "ghp_test" {
			return nil, errors.New("expected GITHUB_TOKEN from environment")
		}
		return githubtracker.NewTaskManager(githubtracker.Config{
			Owner:       cfg.Owner,
			Repo:        cfg.Repo,
			Token:       cfg.Token,
			APIEndpoint: server.URL,
			HTTPClient:  server.Client(),
		})
	}
	t.Cleanup(func() {
		newGitHubTaskManager = originalFactory
	})

	profile, err := resolveTrackerProfile(repo, "", rootID, os.Getenv)
	if err != nil {
		t.Fatalf("resolve tracker profile: %v", err)
	}
	if profile.Name != profileName {
		t.Fatalf("expected profile %q, got %q", profileName, profile.Name)
	}
	if profile.Tracker.Type != trackerTypeGitHub {
		t.Fatalf("expected tracker type %q, got %q", trackerTypeGitHub, profile.Tracker.Type)
	}

	taskManager, err := buildTaskManagerForTracker(repo, profile)
	if err != nil {
		t.Fatalf("build github task manager from profile: %v", err)
	}
	codexRunner := codex.NewCLIRunnerAdapter(writeFakeCodexBinary(t), nil)
	fakeVCS := &fakeVCS{}

	originalStdout := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writePipe
	defer func() {
		os.Stdout = originalStdout
	}()

	runErr := runWithComponents(context.Background(), runConfig{
		repoRoot:    repo,
		rootID:      rootID,
		backend:     backendCodex,
		profile:     profile.Name,
		trackerType: profile.Tracker.Type,
		model:       "openai/gpt-5.3-codex",
		maxTasks:    1,
		stream:      true,
	}, taskManager, codexRunner, fakeVCS)
	if runErr != nil {
		t.Fatalf("run failed: %v", runErr)
	}
	if err := writePipe.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	raw, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if err := readPipe.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	events := decodeNDJSONEvents(t, raw)
	sawRunStarted := false
	sawCodexBackend := false
	sawGitHubTracker := false
	sawGitHubProfile := false
	sawCodexRunnerFinished := false
	sawReviewPassVerdict := false
	sawIsolatedClonePath := false
	for _, event := range events {
		if event.Type == contracts.EventTypeRunStarted {
			sawRunStarted = true
			if event.Metadata["backend"] == backendCodex {
				sawCodexBackend = true
			}
			if event.Metadata["tracker"] == trackerTypeGitHub {
				sawGitHubTracker = true
			}
			if event.Metadata["profile"] == profileName {
				sawGitHubProfile = true
			}
			continue
		}
		if event.Type == contracts.EventTypeRunnerStarted && strings.TrimSpace(event.ClonePath) != "" && strings.TrimSpace(event.ClonePath) != repo {
			sawIsolatedClonePath = true
		}
		if event.Type != contracts.EventTypeRunnerFinished {
			continue
		}
		if event.Metadata["backend"] == backendCodex {
			sawCodexRunnerFinished = true
		}
		if event.Metadata["backend"] == backendCodex && event.Metadata["review_verdict"] == "pass" {
			sawReviewPassVerdict = true
		}
	}
	if !sawRunStarted {
		t.Fatalf("expected run_started event in stream, got %q", string(raw))
	}
	if !sawCodexBackend {
		t.Fatalf("expected run_started metadata backend=%q, got %q", backendCodex, string(raw))
	}
	if !sawGitHubTracker {
		t.Fatalf("expected run_started metadata tracker=%q, got %q", trackerTypeGitHub, string(raw))
	}
	if !sawGitHubProfile {
		t.Fatalf("expected run_started metadata profile=%q, got %q", profileName, string(raw))
	}
	if !sawCodexRunnerFinished {
		t.Fatalf("expected runner_finished metadata backend=%q, got %q", backendCodex, string(raw))
	}
	if !sawReviewPassVerdict {
		t.Fatalf("expected codex review verdict metadata to include pass, got %q", string(raw))
	}
	if !sawIsolatedClonePath {
		t.Fatalf("expected runner_started clone path to use isolated clone, got %q", string(raw))
	}

	task, err := taskManager.GetTask(context.Background(), issueID)
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}
	if task.Status != contracts.TaskStatusClosed {
		t.Fatalf("expected github issue %q to be closed, got %q", issueID, task.Status)
	}

	stateMu.Lock()
	transitions := append([]string(nil), statusTransitions...)
	stateMu.Unlock()
	if len(transitions) != 2 {
		t.Fatalf("expected exactly two GitHub status transitions, got %#v", transitions)
	}
	if transitions[0] != "open" {
		t.Fatalf("expected first transition to open, got %#v", transitions)
	}
	if transitions[1] != "closed" {
		t.Fatalf("expected second transition to closed, got %#v", transitions)
	}
}

func TestE2E_KimiLinearProfileProcessesAndClosesIssue(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI is required for e2e test")
	}

	repo := t.TempDir()
	runCommand(t, repo, "git", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runCommand(t, repo, "git", "add", "README.md")
	runCommand(t, repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	const (
		rootID      = "proj-linear-kimi-e2e"
		issueID     = "iss-linear-kimi-e2e"
		profileName = "linear-kimi-demo"
	)

	writeTrackerConfigYAML(t, repo, `
default_profile: linear-kimi-demo
profiles:
  linear-kimi-demo:
    tracker:
      type: linear
      linear:
        scope:
          workspace: acme
        auth:
          token_env: LINEAR_TOKEN
`)
	t.Setenv("LINEAR_TOKEN", "lin_api_test")

	type issueState struct {
		Type string
		Name string
	}

	var (
		stateMu           sync.Mutex
		currentState      = issueState{Type: "backlog", Name: "Backlog"}
		statusTransitions []contracts.TaskStatus
	)

	issuePayload := func(state issueState) map[string]any {
		return map[string]any{
			"id":          issueID,
			"title":       "Implement Kimi + Linear e2e demo",
			"description": "End-to-end processing",
			"priority":    1,
			"project":     map[string]any{"id": rootID},
			"parent":      nil,
			"state":       map[string]any{"type": state.Type, "name": state.Name},
			"relations":   map[string]any{"nodes": []any{}},
		}
	}

	writeResponse := func(t *testing.T, w http.ResponseWriter, data any) {
		t.Helper()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
			t.Fatalf("encode GraphQL response: %v", err)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Helper()
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "lin_api_test" {
			t.Fatalf("expected Authorization=lin_api_test, got %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var payload struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode GraphQL request body: %v", err)
		}
		query := payload.Query

		switch {
		case strings.Contains(query, "AuthProbe"):
			writeResponse(t, w, map[string]any{
				"viewer": map[string]any{"id": "usr-1"},
			})
		case strings.Contains(query, "ReadProjectBacklog"):
			if !strings.Contains(query, `project(id: "proj-linear-kimi-e2e")`) {
				t.Fatalf("expected project backlog query for %q, got %q", rootID, query)
			}
			stateMu.Lock()
			state := currentState
			stateMu.Unlock()
			writeResponse(t, w, map[string]any{
				"project": map[string]any{
					"id":   rootID,
					"name": "Linear Kimi e2e project",
					"issues": map[string]any{
						"nodes": []any{issuePayload(state)},
					},
				},
			})
		case strings.Contains(query, "ReadIssueWorkflowStatesForWrite"):
			writeResponse(t, w, map[string]any{
				"issue": map[string]any{
					"id": issueID,
					"team": map[string]any{
						"states": map[string]any{
							"nodes": []any{
								map[string]any{"id": "st-open", "type": "backlog", "name": "Backlog"},
								map[string]any{"id": "st-started", "type": "started", "name": "In Progress"},
								map[string]any{"id": "st-closed", "type": "completed", "name": "Done"},
							},
						},
					},
				},
			})
		case strings.Contains(query, "UpdateIssueWorkflowState"):
			stateMu.Lock()
			switch {
			case strings.Contains(query, `stateId: "st-started"`):
				currentState = issueState{Type: "started", Name: "In Progress"}
				statusTransitions = append(statusTransitions, contracts.TaskStatusInProgress)
			case strings.Contains(query, `stateId: "st-closed"`):
				currentState = issueState{Type: "completed", Name: "Done"}
				statusTransitions = append(statusTransitions, contracts.TaskStatusClosed)
			default:
				stateMu.Unlock()
				t.Fatalf("unexpected workflow-state mutation query: %q", query)
			}
			stateMu.Unlock()
			writeResponse(t, w, map[string]any{
				"issueUpdate": map[string]any{"success": true},
			})
		case strings.Contains(query, "ReadIssue"):
			if !strings.Contains(query, `issue(id: "iss-linear-kimi-e2e")`) {
				t.Fatalf("expected issue query for %q, got %q", issueID, query)
			}
			stateMu.Lock()
			state := currentState
			stateMu.Unlock()
			writeResponse(t, w, map[string]any{
				"issue": issuePayload(state),
			})
		default:
			t.Fatalf("unexpected GraphQL query: %q", query)
		}
	}))
	t.Cleanup(server.Close)

	originalFactory := newLinearTaskManager
	newLinearTaskManager = func(cfg linear.Config) (contracts.TaskManager, error) {
		if cfg.Workspace != "acme" {
			return nil, errors.New("expected workspace acme")
		}
		if cfg.Token != "lin_api_test" {
			return nil, errors.New("expected LINEAR_TOKEN from environment")
		}
		return linear.NewTaskManager(linear.Config{
			Workspace:  cfg.Workspace,
			Token:      cfg.Token,
			Endpoint:   server.URL,
			HTTPClient: server.Client(),
		})
	}
	t.Cleanup(func() {
		newLinearTaskManager = originalFactory
	})

	profile, err := resolveTrackerProfile(repo, "", rootID, os.Getenv)
	if err != nil {
		t.Fatalf("resolve tracker profile: %v", err)
	}
	if profile.Name != profileName {
		t.Fatalf("expected profile %q, got %q", profileName, profile.Name)
	}
	if profile.Tracker.Type != trackerTypeLinear {
		t.Fatalf("expected tracker type %q, got %q", trackerTypeLinear, profile.Tracker.Type)
	}

	taskManager, err := buildTaskManagerForTracker(repo, profile)
	if err != nil {
		t.Fatalf("build linear task manager from profile: %v", err)
	}
	kimiRunner := kimi.NewCLIRunnerAdapter(writeFakeKimiBinary(t), nil)
	fakeVCS := &fakeVCS{}

	originalStdout := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writePipe
	defer func() {
		os.Stdout = originalStdout
	}()

	runErr := runWithComponents(context.Background(), runConfig{
		repoRoot:    repo,
		rootID:      rootID,
		backend:     backendKimi,
		profile:     profile.Name,
		trackerType: profile.Tracker.Type,
		model:       "kimi-k2",
		maxTasks:    1,
		stream:      true,
	}, taskManager, kimiRunner, fakeVCS)
	if runErr != nil {
		t.Fatalf("run failed: %v", runErr)
	}
	if err := writePipe.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	raw, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if err := readPipe.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	events := decodeNDJSONEvents(t, raw)
	sawRunStarted := false
	sawKimiBackend := false
	sawLinearTracker := false
	sawLinearProfile := false
	sawKimiRunnerFinished := false
	sawReviewPassVerdict := false
	for _, event := range events {
		if event.Type == contracts.EventTypeRunStarted {
			sawRunStarted = true
			if event.Metadata["backend"] == backendKimi {
				sawKimiBackend = true
			}
			if event.Metadata["tracker"] == trackerTypeLinear {
				sawLinearTracker = true
			}
			if event.Metadata["profile"] == profileName {
				sawLinearProfile = true
			}
			continue
		}
		if event.Type != contracts.EventTypeRunnerFinished {
			continue
		}
		if event.Metadata["backend"] == backendKimi {
			sawKimiRunnerFinished = true
		}
		if event.Metadata["backend"] == backendKimi && event.Metadata["review_verdict"] == "pass" {
			sawReviewPassVerdict = true
		}
	}
	if !sawRunStarted {
		t.Fatalf("expected run_started event in stream, got %q", string(raw))
	}
	if !sawKimiBackend {
		t.Fatalf("expected run_started metadata backend=%q, got %q", backendKimi, string(raw))
	}
	if !sawLinearTracker {
		t.Fatalf("expected run_started metadata tracker=%q, got %q", trackerTypeLinear, string(raw))
	}
	if !sawLinearProfile {
		t.Fatalf("expected run_started metadata profile=%q, got %q", profileName, string(raw))
	}
	if !sawKimiRunnerFinished {
		t.Fatalf("expected runner_finished metadata backend=%q, got %q", backendKimi, string(raw))
	}
	if !sawReviewPassVerdict {
		t.Fatalf("expected kimi review verdict metadata to include pass, got %q", string(raw))
	}

	task, err := taskManager.GetTask(context.Background(), issueID)
	if err != nil {
		t.Fatalf("get task failed: %v", err)
	}
	if task.Status != contracts.TaskStatusClosed {
		t.Fatalf("expected linear issue %q to be closed, got %q", issueID, task.Status)
	}

	stateMu.Lock()
	transitions := append([]contracts.TaskStatus(nil), statusTransitions...)
	stateMu.Unlock()
	if len(transitions) != 2 {
		t.Fatalf("expected exactly two Linear status transitions, got %#v", transitions)
	}
	if transitions[0] != contracts.TaskStatusInProgress {
		t.Fatalf("expected first transition to in_progress, got %#v", transitions)
	}
	if transitions[1] != contracts.TaskStatusClosed {
		t.Fatalf("expected second transition to closed, got %#v", transitions)
	}
}

func TestE2E_ClaudeConflictRetryPathFinalizesWithLandingOrBlockedTriage(t *testing.T) {
	if _, err := exec.LookPath("tk"); err != nil {
		t.Skip("tk CLI is required for e2e test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git CLI is required for e2e test")
	}

	t.Run("lands after one retry", func(t *testing.T) {
		taskID, taskManager, vcs, events, raw := runClaudeConflictRetryScenario(t, []error{
			errors.New("merge conflict first"),
		})

		if vcs.mergeCalls != 2 {
			t.Fatalf("expected one retry with two merge attempts, got %d", vcs.mergeCalls)
		}

		sawRunStarted := false
		sawClaudeBackend := false
		mergeRetryCount := 0
		sawMergeLanded := false
		sawMergeBlocked := false
		for _, event := range events {
			if event.Type == contracts.EventTypeRunStarted {
				sawRunStarted = true
				if event.Metadata["backend"] == backendClaude {
					sawClaudeBackend = true
				}
			}
			if event.Type == contracts.EventTypeMergeRetry {
				mergeRetryCount++
			}
			if event.Type == contracts.EventTypeMergeLanded {
				sawMergeLanded = true
			}
			if event.Type == contracts.EventTypeMergeBlocked {
				sawMergeBlocked = true
			}
		}

		if !sawRunStarted {
			t.Fatalf("expected run_started event in stream, got %q", raw)
		}
		if !sawClaudeBackend {
			t.Fatalf("expected run_started metadata backend=%q, got %q", backendClaude, raw)
		}
		if mergeRetryCount != 1 {
			t.Fatalf("expected exactly one merge_retry event, got %d from %q", mergeRetryCount, raw)
		}
		if !sawMergeLanded {
			t.Fatalf("expected merge_landed event after retry, got %q", raw)
		}
		if sawMergeBlocked {
			t.Fatalf("did not expect merge_blocked event on landed scenario, got %q", raw)
		}

		task, err := taskManager.GetTask(context.Background(), taskID)
		if err != nil {
			t.Fatalf("get task failed: %v", err)
		}
		if task.Status != contracts.TaskStatusClosed {
			t.Fatalf("expected task to be closed, got %s", task.Status)
		}
	})

	t.Run("blocks with triage metadata after retry exhaustion", func(t *testing.T) {
		_, _, vcs, events, raw := runClaudeConflictRetryScenario(t, []error{
			errors.New("merge conflict first"),
			errors.New("merge conflict second"),
		})

		if vcs.mergeCalls != 2 {
			t.Fatalf("expected one retry with two merge attempts, got %d", vcs.mergeCalls)
		}

		sawRunStarted := false
		sawClaudeBackend := false
		mergeRetryCount := 0
		sawMergeLanded := false
		mergeBlockedReason := ""
		taskFinishedBlocked := false
		taskFinishedReason := ""
		taskDataBlocked := false
		taskDataReason := ""
		for _, event := range events {
			if event.Type == contracts.EventTypeRunStarted {
				sawRunStarted = true
				if event.Metadata["backend"] == backendClaude {
					sawClaudeBackend = true
				}
			}
			switch event.Type {
			case contracts.EventTypeMergeRetry:
				mergeRetryCount++
			case contracts.EventTypeMergeLanded:
				sawMergeLanded = true
			case contracts.EventTypeMergeBlocked:
				mergeBlockedReason = strings.TrimSpace(event.Metadata["triage_reason"])
			case contracts.EventTypeTaskFinished:
				if event.Message == string(contracts.TaskStatusBlocked) {
					taskFinishedBlocked = true
					taskFinishedReason = strings.TrimSpace(event.Metadata["triage_reason"])
				}
			case contracts.EventTypeTaskDataUpdated:
				if event.Metadata["triage_status"] == "blocked" {
					taskDataBlocked = true
					taskDataReason = strings.TrimSpace(event.Metadata["triage_reason"])
				}
			}
		}

		if !sawRunStarted {
			t.Fatalf("expected run_started event in stream, got %q", raw)
		}
		if !sawClaudeBackend {
			t.Fatalf("expected run_started metadata backend=%q, got %q", backendClaude, raw)
		}
		if mergeRetryCount != 1 {
			t.Fatalf("expected exactly one merge_retry event, got %d from %q", mergeRetryCount, raw)
		}
		if sawMergeLanded {
			t.Fatalf("did not expect merge_landed event when both attempts conflict, got %q", raw)
		}
		if mergeBlockedReason == "" {
			t.Fatalf("expected merge_blocked triage_reason metadata, got %q", raw)
		}
		if !strings.Contains(mergeBlockedReason, "merge conflict second") {
			t.Fatalf("expected merge_blocked triage_reason to include final conflict, got %q", mergeBlockedReason)
		}
		if !taskFinishedBlocked {
			t.Fatalf("expected task_finished blocked event, got %q", raw)
		}
		if taskFinishedReason == "" || !strings.Contains(taskFinishedReason, "merge conflict second") {
			t.Fatalf("expected task_finished triage_reason with final conflict, got %q", taskFinishedReason)
		}
		if !taskDataBlocked {
			t.Fatalf("expected task_data_updated with triage_status=blocked, got %q", raw)
		}
		if taskDataReason == "" || !strings.Contains(taskDataReason, "merge conflict second") {
			t.Fatalf("expected task_data_updated triage_reason with final conflict, got %q", taskDataReason)
		}
	})
}

func runClaudeConflictRetryScenario(t *testing.T, mergeErrs []error) (string, *tk.TaskManager, *fakeVCS, []contracts.Event, string) {
	t.Helper()

	repo := t.TempDir()
	runCommand(t, repo, "git", "init")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runCommand(t, repo, "git", "add", "README.md")
	runCommand(t, repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	runner := localRunner{dir: repo}
	rootID := mustCreateTicket(t, runner, "Roadmap", "epic", "0", "")
	taskID := mustCreateTicket(t, runner, "Conflict retry task", "task", "0", rootID)

	taskManager := tk.NewTaskManager(runner)
	fakeClaude := claude.NewCLIRunnerAdapter(writeFakeClaudeBinary(t), nil)
	fakeVCS := &fakeVCS{mergeErrs: append([]error(nil), mergeErrs...)}

	originalStdout := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = writePipe
	defer func() {
		os.Stdout = originalStdout
	}()

	runErr := runWithComponents(context.Background(), runConfig{
		repoRoot:    repo,
		rootID:      rootID,
		backend:     backendClaude,
		model:       "claude-3-5-sonnet",
		maxTasks:    1,
		concurrency: 1,
		stream:      true,
	}, taskManager, fakeClaude, fakeVCS)
	if runErr != nil {
		t.Fatalf("run failed: %v", runErr)
	}
	if err := writePipe.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	raw, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if err := readPipe.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}

	events := decodeNDJSONEvents(t, raw)
	return taskID, taskManager, fakeVCS, events, string(raw)
}

func decodeNDJSONEvents(t *testing.T, raw []byte) []contracts.Event {
	t.Helper()

	decoder := contracts.NewEventDecoder(bytes.NewReader(raw))
	events := []contracts.Event{}
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to decode NDJSON stream: %v", err)
		}
		events = append(events, event)
	}
	return events
}

type fakeAgentRunner struct {
	results  []contracts.RunnerResult
	index    int
	requests []contracts.RunnerRequest
}

type parallelFakeAgentRunner struct {
	mu      sync.Mutex
	results []contracts.RunnerResult
	index   int
	delay   time.Duration
}

func (f *parallelFakeAgentRunner) Run(_ context.Context, _ contracts.RunnerRequest) (contracts.RunnerResult, error) {
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.index >= len(f.results) {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "missing result"}, nil
	}
	result := f.results[f.index]
	f.index++
	return result, nil
}

func (f *fakeAgentRunner) Run(_ context.Context, request contracts.RunnerRequest) (contracts.RunnerResult, error) {
	f.requests = append(f.requests, request)
	if f.index >= len(f.results) {
		return contracts.RunnerResult{Status: contracts.RunnerResultFailed, Reason: "missing result"}, nil
	}
	result := f.results[f.index]
	f.index++
	return result, nil
}

type fakeVCS struct {
	mergeErrs  []error
	mergeCalls int
}

func (f *fakeVCS) EnsureMain(context.Context) error { return nil }
func (f *fakeVCS) CreateTaskBranch(_ context.Context, taskID string) (string, error) {
	return "task/" + taskID, nil
}
func (f *fakeVCS) Checkout(context.Context, string) error            { return nil }
func (f *fakeVCS) CommitAll(context.Context, string) (string, error) { return "", nil }
func (f *fakeVCS) MergeToMain(context.Context, string) error {
	f.mergeCalls++
	if len(f.mergeErrs) > 0 {
		err := f.mergeErrs[0]
		f.mergeErrs = f.mergeErrs[1:]
		return err
	}
	return nil
}
func (f *fakeVCS) PushBranch(context.Context, string) error { return nil }
func (f *fakeVCS) PushMain(context.Context) error           { return nil }

func mustCreateTicket(t *testing.T, runner localRunner, title string, issueType string, priority string, parent string) string {
	t.Helper()
	args := []string{"tk", "create", title, "-t", issueType, "-p", priority}
	if parent != "" {
		args = append(args, "--parent", parent)
	}
	out, err := runner.Run(args...)
	if err != nil {
		t.Fatalf("create ticket failed: %v (%s)", err, out)
	}
	return strings.TrimSpace(out)
}

func runCommand(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v (%s)", name, args, err, string(output))
	}
}

func writeFakeCodexBinary(t *testing.T) string {
	t.Helper()
	binaryPath := filepath.Join(t.TempDir(), "fake-codex")
	script := "#!/bin/sh\nprintf 'REVIEW_VERDICT: pass\\n'\n"
	if err := os.WriteFile(binaryPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex binary: %v", err)
	}
	return binaryPath
}

func writeFakeClaudeBinary(t *testing.T) string {
	t.Helper()
	binaryPath := filepath.Join(t.TempDir(), "fake-claude")
	script := "#!/bin/sh\nprintf 'REVIEW_VERDICT: pass\\n'\n"
	if err := os.WriteFile(binaryPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude binary: %v", err)
	}
	return binaryPath
}

func writeFakeKimiBinary(t *testing.T) string {
	t.Helper()
	binaryPath := filepath.Join(t.TempDir(), "fake-kimi")
	script := "#!/bin/sh\nprintf 'REVIEW_VERDICT: pass\\n'\n"
	if err := os.WriteFile(binaryPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kimi binary: %v", err)
	}
	return binaryPath
}
