package webhook

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const defaultWorkerPollInterval = 250 * time.Millisecond

type JobProcessor interface {
	Process(context.Context, Job) error
}

type JobProcessorFunc func(context.Context, Job) error

func (f JobProcessorFunc) Process(ctx context.Context, job Job) error {
	return f(ctx, job)
}

type WorkerConfig struct {
	QueuePath    string
	PollInterval time.Duration
	Once         bool
}

type Worker struct {
	queuePath    string
	pollInterval time.Duration
	once         bool
	processor    JobProcessor

	offset int64
	seen   map[string]struct{}
}

func NewWorker(cfg WorkerConfig, processor JobProcessor) (*Worker, error) {
	queuePath := strings.TrimSpace(cfg.QueuePath)
	if queuePath == "" {
		return nil, fmt.Errorf("queue path is required")
	}
	if processor == nil {
		return nil, fmt.Errorf("job processor is required")
	}

	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultWorkerPollInterval
	}

	return &Worker{
		queuePath:    queuePath,
		pollInterval: pollInterval,
		once:         cfg.Once,
		processor:    processor,
		seen:         map[string]struct{}{},
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	if w.once {
		return w.consumeAvailable(ctx)
	}

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		if err := w.consumeAvailable(ctx); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *Worker) consumeAvailable(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}

	stat, err := os.Stat(w.queuePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat queue file: %w", err)
	}
	if stat.Size() < w.offset {
		w.offset = 0
	}

	file, err := os.Open(w.queuePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open queue file: %w", err)
	}
	defer file.Close()

	if _, err := file.Seek(w.offset, io.SeekStart); err != nil {
		return fmt.Errorf("seek queue file: %w", err)
	}

	reader := bufio.NewReader(file)
	var advanced int64
	for {
		if ctx.Err() != nil {
			w.offset += advanced
			return nil
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				w.offset += advanced
				return nil
			}
			return fmt.Errorf("read queue file: %w", err)
		}

		advanced += int64(len(line))
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		job, err := decodeQueuedJobLine(line)
		if err != nil {
			return err
		}
		if w.isDuplicate(job) {
			continue
		}
		if err := w.processor.Process(ctx, job); err != nil {
			return fmt.Errorf("process queued job %q: %w", job.ID, err)
		}
	}
}

func decodeQueuedJobLine(line []byte) (Job, error) {
	var job Job
	if err := json.Unmarshal(line, &job); err != nil {
		return Job{}, fmt.Errorf("decode queued job line: %w", err)
	}

	if strings.TrimSpace(job.ID) == "" && strings.TrimSpace(job.IdempotencyKey) == "" && strings.TrimSpace(job.SessionID) == "" {
		return Job{}, fmt.Errorf("decode queued job line: missing job identifiers")
	}
	if job.ContractVersion == 0 {
		job.ContractVersion = JobContractVersion1
	}
	if job.ContractVersion != JobContractVersion1 {
		return Job{}, fmt.Errorf("unsupported queued job contract version %d", job.ContractVersion)
	}
	return job, nil
}

func (w *Worker) isDuplicate(job Job) bool {
	key := strings.TrimSpace(job.IdempotencyKey)
	if key == "" {
		return false
	}
	if _, ok := w.seen[key]; ok {
		return true
	}
	w.seen[key] = struct{}{}
	return false
}
