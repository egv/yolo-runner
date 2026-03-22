package contracts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type BackendLogKind string

const (
	BackendLogStderr        BackendLogKind = "stderr"
	BackendLogProtocolTrace BackendLogKind = "protocol"
)

type HTTPReadinessCheck struct {
	Endpoint string
	Method   string
	Headers  map[string]string
}

type StdioReadinessCheck struct {
	Command string
	Run     func(context.Context, string, ...string) ([]byte, error)
}

func FinalizeRunError(ctx context.Context, runErr error) error {
	if runErr == nil && ctx != nil && ctx.Err() != nil {
		runErr = ctx.Err()
	}
	if runErr == nil || ctx == nil {
		return runErr
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		if hasDetailedDeadlineExceeded(runErr) {
			return runErr
		}
		return context.DeadlineExceeded
	}
	if errors.Is(ctx.Err(), context.Canceled) && errors.Is(runErr, context.Canceled) {
		return context.Canceled
	}
	return runErr
}

func hasDetailedDeadlineExceeded(err error) bool {
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return strings.TrimSpace(err.Error()) != context.DeadlineExceeded.Error()
}

func BuildRunnerArtifacts(backend string, request RunnerRequest, result RunnerResult, extras map[string]string) map[string]string {
	artifacts := map[string]string{
		"backend": strings.TrimSpace(backend),
		"status":  string(result.Status),
	}
	if model := strings.TrimSpace(request.Model); model != "" {
		artifacts["model"] = model
	}
	if mode := strings.TrimSpace(string(request.Mode)); mode != "" {
		artifacts["mode"] = mode
	}
	if logPath := strings.TrimSpace(result.LogPath); logPath != "" {
		artifacts["log_path"] = logPath
	}
	if !result.StartedAt.IsZero() {
		artifacts["started_at"] = result.StartedAt.UTC().Format(time.RFC3339)
	}
	if !result.FinishedAt.IsZero() {
		artifacts["finished_at"] = result.FinishedAt.UTC().Format(time.RFC3339)
	}
	if reason := strings.TrimSpace(result.Reason); reason != "" {
		artifacts["reason"] = reason
	}
	if request.Metadata != nil {
		if clonePath := strings.TrimSpace(request.Metadata["clone_path"]); clonePath != "" {
			artifacts["clone_path"] = clonePath
		}
	}
	for key, value := range extras {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		artifacts[key] = value
	}
	if len(artifacts) == 0 {
		return nil
	}
	return artifacts
}

func NewRunnerOutputProgress(source string, line string, timestamp time.Time) (RunnerProgress, bool) {
	normalized := normalizeRuntimeLine(line)
	if normalized == "" {
		return RunnerProgress{}, false
	}
	source = strings.TrimSpace(source)
	if source == "stderr" {
		normalized = "stderr: " + normalized
	}
	return RunnerProgress{
		Type:      string(EventTypeRunnerOutput),
		Message:   normalized,
		Metadata:  map[string]string{"source": source},
		Timestamp: timestamp.UTC(),
	}, true
}

func BackendLogSidecarPath(logPath string, kind BackendLogKind) string {
	base := strings.TrimSpace(logPath)
	if base == "" {
		return ""
	}
	if strings.HasSuffix(base, ".jsonl") {
		base = strings.TrimSuffix(base, ".jsonl")
	}
	switch kind {
	case BackendLogProtocolTrace:
		return base + ".protocol.log"
	case BackendLogStderr:
		fallthrough
	default:
		return base + ".stderr.log"
	}
}

func CheckHTTPReadiness(ctx context.Context, client *http.Client, check HTTPReadinessCheck) error {
	endpoint := strings.TrimSpace(check.Endpoint)
	if endpoint == "" {
		return fmt.Errorf("health endpoint is empty")
	}
	if client == nil {
		client = http.DefaultClient
	}
	method := strings.ToUpper(strings.TrimSpace(check.Method))
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	for key, value := range check.Headers {
		req.Header.Set(strings.TrimSpace(key), strings.TrimSpace(value))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("health endpoint returned %d", resp.StatusCode)
	}
	return nil
}

func CheckStdioReadiness(ctx context.Context, check StdioReadinessCheck) error {
	command := strings.TrimSpace(check.Command)
	if command == "" {
		return fmt.Errorf("health command is empty")
	}
	if check.Run == nil {
		return fmt.Errorf("health command runner is required")
	}
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return fmt.Errorf("health command is empty")
	}
	output, err := check.Run(ctx, parts[0], parts[1:]...)
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed != "" {
			return fmt.Errorf("health command failed: %w: %s", err, trimmed)
		}
		return fmt.Errorf("health command failed: %w", err)
	}
	return nil
}

func WithOptionalTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func normalizeRuntimeLine(raw string) string {
	return strings.TrimSpace(strings.ReplaceAll(raw, "\x00", ""))
}
