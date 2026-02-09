test:
	go test ./...

smoke-agent-tui:
	go test ./cmd/yolo-agent ./cmd/yolo-tui

build:
	mkdir -p bin
	go build -o bin/yolo-runner ./cmd/yolo-runner
