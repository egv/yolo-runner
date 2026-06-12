package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/contracts"
	"github.com/egv/yolo-runner/v2/internal/executor"
	"github.com/egv/yolo-runner/v2/internal/workitem"
)

type WorkDispatcher interface {
	Submit(ctx context.Context, request WorkDispatchRequest) (WorkHandle, error)
	AwaitResult(ctx context.Context, handle WorkHandle) (workitem.ImplementResult, error)
}

type WorkDispatchRequest struct {
	Task     contracts.Task
	Payload  workitem.ImplementPayload
	Priority int
	Executor *executor.Executor
}

type WorkHandle struct {
	ID string

	taskID   string
	executor *executor.Executor
	result   workitem.ImplementResult
	err      error
}

type inProcessDispatcher struct{}

func newInProcessDispatcher() *inProcessDispatcher {
	return &inProcessDispatcher{}
}

func (d *inProcessDispatcher) Submit(ctx context.Context, request WorkDispatchRequest) (WorkHandle, error) {
	if request.Executor == nil {
		return WorkHandle{}, fmt.Errorf("executor is required")
	}
	result, err := request.Executor.Execute(ctx, request.Payload)
	handleID := strings.TrimSpace(request.Payload.TaskID)
	if handleID == "" {
		handleID = strings.TrimSpace(request.Task.ID)
	}
	return WorkHandle{
		ID:     handleID,
		taskID: handleID,
		result: result,
		err:    err,
	}, nil
}

func (d *inProcessDispatcher) AwaitResult(_ context.Context, handle WorkHandle) (workitem.ImplementResult, error) {
	return handle.result, handle.err
}
