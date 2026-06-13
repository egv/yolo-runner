package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egv/yolo-runner/v2/internal/agent"
)

const defaultEventsFollowPollInterval = 250 * time.Millisecond

type eventsFollowConfig struct {
	eventsDir    string
	since        time.Time
	once         bool
	pollInterval time.Duration
	out          io.Writer
}

type eventsFollowState struct {
	offset  int64
	pending string
	lines   int
}

type eventsFollowRecord struct {
	ts   time.Time
	path string
	line int
	raw  string
}

func eventsCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yolo-agent events <follow> [flags]")
		return 1
	}

	switch args[0] {
	case "follow":
		return eventsFollowCommand(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown events command: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "usage: yolo-agent events <follow> [flags]")
		return 1
	}
}

func eventsFollowCommand(args []string) int {
	fs := flag.NewFlagSet("yolo-agent events follow", flag.ContinueOnError)
	sinceRaw := fs.String("since", "", "Only emit events at or after this RFC3339 timestamp or duration ago")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected events follow argument: %s\n", fs.Arg(0))
		return 1
	}

	since, err := parseEventsFollowSince(*sinceRaw, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(os.Stderr, agent.FormatActionableError(err))
		return 1
	}
	if err := runEventsFollow(context.Background(), eventsFollowConfig{
		since: since,
		out:   os.Stdout,
	}); err != nil {
		fmt.Fprintln(os.Stderr, agent.FormatActionableError(err))
		return 1
	}
	return 0
}

func parseEventsFollowSince(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if duration, err := time.ParseDuration(raw); err == nil {
		return now.Add(-duration).UTC(), nil
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("parse --since %q: expected duration or RFC3339 timestamp", raw)
}

func runEventsFollow(ctx context.Context, cfg eventsFollowConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	out := cfg.out
	if out == nil {
		out = os.Stdout
	}
	eventsDir, err := resolveEventsFollowDir(cfg.eventsDir)
	if err != nil {
		return err
	}
	pollInterval := cfg.pollInterval
	if pollInterval <= 0 {
		pollInterval = defaultEventsFollowPollInterval
	}

	states := map[string]*eventsFollowState{}
	for {
		records, err := readEventsFollowBatch(eventsDir, cfg.since, states)
		if err != nil {
			return err
		}
		if err := writeEventsFollowRecords(out, records); err != nil {
			return err
		}
		if cfg.once {
			return nil
		}
		if err := waitEventsFollowPoll(ctx, pollInterval); err != nil {
			return err
		}
	}
}

func resolveEventsFollowDir(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return configured, nil
	}
	return defaultYoloRunnerEventsDir()
}

func defaultYoloRunnerEventsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		if err == nil {
			err = errors.New("home directory is empty")
		}
		return "", fmt.Errorf("resolve events directory: %w", err)
	}
	return filepath.Join(home, ".yolo-runner", "events"), nil
}

func defaultYoloRunnerEventsDirOrEmpty() string {
	eventsDir, err := defaultYoloRunnerEventsDir()
	if err != nil {
		return ""
	}
	return eventsDir
}

func readEventsFollowBatch(eventsDir string, since time.Time, states map[string]*eventsFollowState) ([]eventsFollowRecord, error) {
	if states == nil {
		states = map[string]*eventsFollowState{}
	}
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read events dir %s: %w", eventsDir, err)
	}

	records := []eventsFollowRecord{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(eventsDir, entry.Name())
		fileRecords, err := readEventsFollowFile(path, since, states)
		if err != nil {
			return nil, err
		}
		records = append(records, fileRecords...)
	}
	sort.SliceStable(records, func(i, j int) bool {
		if !records[i].ts.Equal(records[j].ts) {
			return records[i].ts.Before(records[j].ts)
		}
		if records[i].path != records[j].path {
			return records[i].path < records[j].path
		}
		return records[i].line < records[j].line
	})
	return records, nil
}

func readEventsFollowFile(path string, since time.Time, states map[string]*eventsFollowState) ([]eventsFollowRecord, error) {
	state := states[path]
	if state == nil {
		state = &eventsFollowState{}
		states[path] = state
	}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			delete(states, path)
			return nil, nil
		}
		return nil, fmt.Errorf("stat events file %s: %w", path, err)
	}
	if info.Size() < state.offset {
		state.offset = 0
		state.pending = ""
		state.lines = 0
	}
	if info.Size() == state.offset {
		return nil, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open events file %s: %w", path, err)
	}
	defer file.Close()

	if _, err := file.Seek(state.offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek events file %s: %w", path, err)
	}
	chunk, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read events file %s: %w", path, err)
	}
	state.offset += int64(len(chunk))

	text := state.pending + string(chunk)
	state.pending = ""
	if !strings.HasSuffix(text, "\n") {
		if idx := strings.LastIndexByte(text, '\n'); idx >= 0 {
			state.pending = text[idx+1:]
			text = text[:idx+1]
		} else {
			state.pending = text
			return nil, nil
		}
	}

	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	records := []eventsFollowRecord{}
	for scanner.Scan() {
		state.lines++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		ts, ok := parseEventsFollowRecordTimestamp(raw)
		if !ok || (!since.IsZero() && ts.Before(since)) {
			continue
		}
		records = append(records, eventsFollowRecord{
			ts:   ts,
			path: path,
			line: state.lines,
			raw:  raw,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan events file %s: %w", path, err)
	}
	return records, nil
}

func parseEventsFollowRecordTimestamp(raw string) (time.Time, bool) {
	var payload struct {
		TS string `json:"ts"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(payload.TS))
	if err != nil {
		return time.Time{}, false
	}
	return ts.UTC(), true
}

func writeEventsFollowRecords(out io.Writer, records []eventsFollowRecord) error {
	for _, record := range records {
		if _, err := fmt.Fprintln(out, record.raw); err != nil {
			return fmt.Errorf("write event stream: %w", err)
		}
	}
	return nil
}

func waitEventsFollowPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
