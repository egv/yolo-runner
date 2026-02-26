package distributed

type EventSubjects struct {
	Register               string
	Heartbeat              string
	Offline                string
	TaskDispatch           string
	TaskResult             string
	ServiceRequest         string
	ServiceResult          string
	TaskGraphSnapshot      string
	TaskGraphDiff          string
	TaskStatusUpdate       string
	TaskStatusUpdateAck    string
	TaskStatusUpdateReject string
	MonitorEvent           string
}

func DefaultEventSubjects(prefix string) EventSubjects {
	if prefix == "" {
		prefix = "yolo"
	}
	return EventSubjects{
		Register:               prefix + ".executor.register",
		Heartbeat:              prefix + ".executor.heartbeat",
		Offline:                prefix + ".executor.offline",
		TaskDispatch:           prefix + ".task.dispatch",
		TaskResult:             prefix + ".task.result",
		ServiceRequest:         prefix + ".service.request",
		ServiceResult:          prefix + ".service.response",
		TaskGraphSnapshot:      prefix + ".task_graph.snapshot",
		TaskGraphDiff:          prefix + ".task_graph.diff",
		TaskStatusUpdate:       prefix + ".task_status.update",
		TaskStatusUpdateAck:    prefix + ".task_status.ack",
		TaskStatusUpdateReject: prefix + ".task_status.reject",
		MonitorEvent:           prefix + ".monitor.event",
	}
}
