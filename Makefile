test:
	go test ./...

smoke-agent-tui:
	go test ./cmd/yolo-agent ./cmd/yolo-tui

smoke-event-stream:
	$(MAKE) smoke-agent-tui

build:
	mkdir -p bin
	go build -o bin/yolo-runner ./cmd/yolo-runner
	go build -o bin/yolo-agent ./cmd/yolo-agent
	go build -o bin/yolo-task ./cmd/yolo-task
	go build -o bin/yolo-tui ./cmd/yolo-tui
