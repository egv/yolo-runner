package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTrackerConfigServiceLoadModelDefaultsWhenConfigMissing(t *testing.T) {
	svc := newTrackerConfigService()

	model, err := svc.LoadModel(t.TempDir())
	if err != nil {
		t.Fatalf("expected missing config to fall back to defaults, got %v", err)
	}
	if model.DefaultProfile != defaultProfileName {
		t.Fatalf("expected default profile %q, got %q", defaultProfileName, model.DefaultProfile)
	}
	profile, ok := model.Profiles[defaultProfileName]
	if !ok {
		t.Fatalf("expected default profile %q to exist", defaultProfileName)
	}
	if profile.Tracker.Type != trackerTypeTK {
		t.Fatalf("expected default tracker type %q, got %q", trackerTypeTK, profile.Tracker.Type)
	}
}

func TestTrackerConfigServiceLoadModelRejectsInvalidYAML(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
      tk: [
`)

	svc := newTrackerConfigService()
	_, err := svc.LoadModel(repoRoot)
	if err == nil {
		t.Fatalf("expected invalid YAML to fail")
	}
	if !strings.Contains(err.Error(), "cannot parse config file") {
		t.Fatalf("expected parse failure, got %q", err.Error())
	}
}

func TestTrackerConfigServiceLoadModelRejectsUnknownFields(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
unexpected_key: true
`)

	svc := newTrackerConfigService()
	_, err := svc.LoadModel(repoRoot)
	if err == nil {
		t.Fatalf("expected unknown field to fail")
	}
	if !strings.Contains(err.Error(), "cannot parse config file") {
		t.Fatalf("expected parse failure, got %q", err.Error())
	}
}

func TestTrackerConfigServiceResolveAgentDefaultsRejectsBadNumber(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  concurrency: 0
`)

	svc := newTrackerConfigService()
	_, err := svc.ResolveAgentDefaults(repoRoot)
	if err == nil {
		t.Fatalf("expected invalid agent defaults to fail")
	}
	if !strings.Contains(err.Error(), "agent.concurrency") {
		t.Fatalf("expected numeric validation error, got %q", err.Error())
	}
}

func TestTrackerConfigServiceResolveAgentDefaultsRejectsBadDuration(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  runner_timeout: soon
`)

	svc := newTrackerConfigService()
	_, err := svc.ResolveAgentDefaults(repoRoot)
	if err == nil {
		t.Fatalf("expected invalid duration to fail")
	}
	if !strings.Contains(err.Error(), "agent.runner_timeout") {
		t.Fatalf("expected duration validation error, got %q", err.Error())
	}
}

func TestTrackerConfigServiceResolveAgentDefaultsRejectsUnsupportedBackend(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
agent:
  backend: unsupported
`)

	svc := newTrackerConfigService()
	_, err := svc.ResolveAgentDefaults(repoRoot)
	if err == nil {
		t.Fatalf("expected unsupported backend to fail")
	}
	if !strings.Contains(err.Error(), "agent.backend") {
		t.Fatalf("expected backend field guidance, got %q", err.Error())
	}
}

func TestTrackerConfigServiceResolveTrackerAgentConfigAcceptsFieldsAndDefaults(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
tracker_agent:
  poll_interval: 45s
  lock_path: locks/tracker-agent.lock
  labels:
    ready: custom-ready
    in_progress: custom-running
  status_transitions:
    in_progress: start
    completed: finish
    completed_resolution: done
    blocked: ""
`)

	svc := newTrackerConfigService()
	cfg, err := svc.ResolveTrackerAgentConfig(repoRoot)
	if err != nil {
		t.Fatalf("expected tracker_agent config to resolve, got %v", err)
	}
	if cfg.PollInterval != 45*time.Second {
		t.Fatalf("expected poll interval 45s, got %s", cfg.PollInterval)
	}
	if got, want := cfg.LockPath, filepath.Join(repoRoot, "locks", "tracker-agent.lock"); got != want {
		t.Fatalf("expected lock path %q, got %q", want, got)
	}
	if cfg.Labels.Ready != "custom-ready" {
		t.Fatalf("expected custom ready label, got %q", cfg.Labels.Ready)
	}
	if cfg.Labels.InProgress != "custom-running" {
		t.Fatalf("expected custom in-progress label, got %q", cfg.Labels.InProgress)
	}
	if cfg.Labels.Completed != "yolo-agent-completed" {
		t.Fatalf("expected default completed label, got %q", cfg.Labels.Completed)
	}
	if cfg.Labels.Blocked != "yolo-agent-blocked" {
		t.Fatalf("expected default blocked label, got %q", cfg.Labels.Blocked)
	}
	if cfg.Labels.Failed != "yolo-agent-failed" {
		t.Fatalf("expected default failed label, got %q", cfg.Labels.Failed)
	}
	if cfg.StatusTransitions.InProgress != "start" {
		t.Fatalf("expected custom in-progress transition, got %q", cfg.StatusTransitions.InProgress)
	}
	if cfg.StatusTransitions.Completed != "finish" {
		t.Fatalf("expected custom completed transition, got %q", cfg.StatusTransitions.Completed)
	}
	if cfg.StatusTransitions.CompletedResolution != "done" {
		t.Fatalf("expected custom completed resolution, got %q", cfg.StatusTransitions.CompletedResolution)
	}
	if cfg.StatusTransitions.Blocked != "" {
		t.Fatalf("expected explicitly disabled blocked transition, got %q", cfg.StatusTransitions.Blocked)
	}
}

func TestTrackerConfigServiceResolveTrackerAgentConfigDefaultsWhenOmitted(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
`)

	svc := newTrackerConfigService()
	cfg, err := svc.ResolveTrackerAgentConfig(repoRoot)
	if err != nil {
		t.Fatalf("expected default tracker_agent config to resolve, got %v", err)
	}
	if cfg.PollInterval != 30*time.Second {
		t.Fatalf("expected default poll interval 30s, got %s", cfg.PollInterval)
	}
	if got, want := cfg.LockPath, filepath.Join(repoRoot, ".yolo-runner", "tracker-agent.lock"); got != want {
		t.Fatalf("expected default lock path %q, got %q", want, got)
	}
	if cfg.Labels.Ready != "yolo-agent-ready" {
		t.Fatalf("expected default ready label, got %q", cfg.Labels.Ready)
	}
	if cfg.Labels.InProgress != "yolo-agent-in-progress" {
		t.Fatalf("expected default in-progress label, got %q", cfg.Labels.InProgress)
	}
	if cfg.Labels.Completed != "yolo-agent-completed" {
		t.Fatalf("expected default completed label, got %q", cfg.Labels.Completed)
	}
	if cfg.Labels.Blocked != "yolo-agent-blocked" {
		t.Fatalf("expected default blocked label, got %q", cfg.Labels.Blocked)
	}
	if cfg.Labels.Failed != "yolo-agent-failed" {
		t.Fatalf("expected default failed label, got %q", cfg.Labels.Failed)
	}
	if cfg.StatusTransitions.InProgress != "inProgress" {
		t.Fatalf("expected default in-progress transition, got %q", cfg.StatusTransitions.InProgress)
	}
	if cfg.StatusTransitions.Completed != "closed" {
		t.Fatalf("expected default completed transition, got %q", cfg.StatusTransitions.Completed)
	}
	if cfg.StatusTransitions.CompletedResolution != "fixed" {
		t.Fatalf("expected default completed resolution, got %q", cfg.StatusTransitions.CompletedResolution)
	}
	if cfg.StatusTransitions.Ready != "" || cfg.StatusTransitions.Blocked != "" || cfg.StatusTransitions.Failed != "" {
		t.Fatalf("expected ready/blocked/failed transitions disabled by default, got %#v", cfg.StatusTransitions)
	}
}

func TestTrackerConfigServiceResolveArcReviewWatchConfigAcceptsValidBlock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
arc_review_watch:
  poll_interval: 20s
  state_path: state/arc-review-watch.json
  reviewer: alice
  allow_ship: true
  objects_base_dir: ~/.cache/yolo/pr-objects
  mounts_base_dir: ~/.cache/yolo/pr-mounts
`)

	svc := newTrackerConfigService()
	cfg, err := svc.ResolveArcReviewWatchConfig(repoRoot)
	if err != nil {
		t.Fatalf("expected arc_review_watch config to resolve, got %v", err)
	}
	if cfg.PollInterval != 20*time.Second {
		t.Fatalf("expected poll interval 20s, got %s", cfg.PollInterval)
	}
	if got, want := cfg.StatePath, filepath.Join(repoRoot, "state", "arc-review-watch.json"); got != want {
		t.Fatalf("expected state path %q, got %q", want, got)
	}
	if got := cfg.Reviewer; got != "alice" {
		t.Fatalf("expected reviewer alice, got %q", got)
	}
	if !cfg.AllowShip {
		t.Fatalf("expected allow_ship to parse as true")
	}
	if got, want := cfg.ObjectsBaseDir, filepath.Join(home, ".cache", "yolo", "pr-objects"); got != want {
		t.Fatalf("expected objects base dir %q, got %q", want, got)
	}
	if got, want := cfg.MountsBaseDir, filepath.Join(home, ".cache", "yolo", "pr-mounts"); got != want {
		t.Fatalf("expected mounts base dir %q, got %q", want, got)
	}
}

func TestTrackerConfigServiceResolveLandingConfigDefaultsToGitWhenOmitted(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
`)

	svc := newTrackerConfigService()
	cfg, err := svc.ResolveLandingConfig(repoRoot)
	if err != nil {
		t.Fatalf("expected default landing config to resolve, got %v", err)
	}
	if cfg.Type != "git" {
		t.Fatalf("expected default landing type git, got %q", cfg.Type)
	}
	if cfg.TitleTemplate != "" {
		t.Fatalf("expected empty default title template, got %q", cfg.TitleTemplate)
	}
}

func TestTrackerConfigServiceResolveLandingConfigAcceptsArcPRAndTitleTemplate(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
landing:
  type: arc-pr
  title_template: "Land {{ .TaskID }}: {{ .TaskTitle }}"
`)

	svc := newTrackerConfigService()
	cfg, err := svc.ResolveLandingConfig(repoRoot)
	if err != nil {
		t.Fatalf("expected arc-pr landing config to resolve, got %v", err)
	}
	if cfg.Type != "arc-pr" {
		t.Fatalf("expected landing type arc-pr, got %q", cfg.Type)
	}
	if cfg.TitleTemplate != "Land {{ .TaskID }}: {{ .TaskTitle }}" {
		t.Fatalf("expected title template to parse, got %q", cfg.TitleTemplate)
	}
}

func TestTrackerConfigServiceResolveLandingConfigRejectsUnsupportedType(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: tk
landing:
  type: merge-queue
`)

	svc := newTrackerConfigService()
	_, err := svc.ResolveLandingConfig(repoRoot)
	if err == nil {
		t.Fatalf("expected unsupported landing type to fail")
	}
	if !strings.Contains(err.Error(), "landing.type") {
		t.Fatalf("expected landing type validation error, got %q", err.Error())
	}
}

func TestTrackerConfigServiceResolveTrackerProfileRejectsMissingAuthToken(t *testing.T) {
	repoRoot := t.TempDir()
	writeTrackerConfigYAML(t, repoRoot, `
profiles:
  default:
    tracker:
      type: linear
      linear:
        scope:
          workspace: anomaly
        auth:
          token_env: LINEAR_TOKEN
`)

	svc := newTrackerConfigService()
	_, err := svc.ResolveTrackerProfile(repoRoot, "", "root-1", func(string) string { return "" })
	if err == nil {
		t.Fatalf("expected missing token validation to fail")
	}
	if !strings.Contains(err.Error(), "missing auth token") {
		t.Fatalf("expected auth token guidance, got %q", err.Error())
	}
}
