package runner

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

type StopState struct {
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	requested  bool
	inProgress string
}

func NewStopState() *StopState {
	ctx, cancel := context.WithCancel(context.Background())
	return &StopState{ctx: ctx, cancel: cancel}
}

func (s *StopState) Context() context.Context {
	if s == nil {
		return context.Background()
	}
	return s.ctx
}

func (s *StopState) Request() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requested {
		return
	}
	s.requested = true
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *StopState) Requested() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requested
}

func (s *StopState) MarkInProgress(issueID string) {
	if s == nil || issueID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inProgress = issueID
}

func (s *StopState) InProgressID() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inProgress
}

func (s *StopState) Watch(stopCh <-chan struct{}) {
	if s == nil || stopCh == nil {
		return
	}
	go func() {
		<-stopCh
		s.Request()
	}()
}

type StopCleanupGit interface {
	StatusPorcelain() (string, error)
	RestoreAll() error
	CleanAll() error
}

type StopCleanupConfig struct {
	Beads   BeadsClient
	Git     StopCleanupGit
	Out     io.Writer
	Confirm func(summary string) (bool, error)
}

func CleanupAfterStop(stop *StopState, config StopCleanupConfig) error {
	if stop == nil || !stop.Requested() {
		return nil
	}
	if config.Beads != nil {
		issueID := stop.InProgressID()
		if issueID != "" {
			if err := config.Beads.UpdateStatus(issueID, "open"); err != nil {
				return err
			}
		}
	}
	if config.Git == nil {
		return nil
	}
	status, err := config.Git.StatusPorcelain()
	if err != nil {
		return err
	}
	out := config.Out
	if out == nil {
		out = io.Discard
	}
	if status != "" {
		fmt.Fprintln(out, status)
	}
	if status == "" {
		return nil
	}
	if config.Confirm == nil {
		return nil
	}
	summary := status
	if !strings.HasSuffix(summary, "\n") {
		summary += "\n"
	}
	summary += "Discard these changes? [y/N]"
	ok, err := config.Confirm(summary)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := config.Git.RestoreAll(); err != nil {
		return err
	}
	if err := config.Git.CleanAll(); err != nil {
		return err
	}
	return nil
}
