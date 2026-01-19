test:
	go test ./...

build:
	mkdir -p bin
	go build -o bin/yolo-runner ./cmd/yolo-runner
