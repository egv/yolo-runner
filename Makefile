test:
	go test ./...

smoke-agent-tui:
	go test ./cmd/yolo-agent ./cmd/yolo-tui
	$(MAKE) smoke-config-commands

smoke-config-commands:
	go test ./cmd/yolo-agent -run 'TestE2E_ConfigCommands_(InitThenValidateHappyPath|ValidateMissingFileFallsBackToDefaults|ValidateInvalidValuesReportsDeterministicDiagnostics|ValidateMissingAuthEnvReportsRemediation)$$' -count=1

smoke-event-stream:
	$(MAKE) smoke-agent-tui

distributed-dev-up:
	docker compose -f dev/distributed/docker-compose.yml up -d redis nats

distributed-dev-down:
	docker compose -f dev/distributed/docker-compose.yml down -v

smoke-distributed-e2e:
	./scripts/distributed-smoke.sh

release-gate-e8:
	go test ./cmd/yolo-agent -run 'TestE2E_(CodexTKConcurrency2LandsViaMergeQueue|ClaudeConflictRetryPathFinalizesWithLandingOrBlockedTriage|KimiLinearProfileProcessesAndClosesIssue|GitHubProfileProcessesAndClosesIssue)$$' -count=1
	go test ./internal/docs -run 'Test(MakefileDefinesE8ReleaseGateChecklistTarget|ReadmeDocumentsE8ReleaseGateChecklist|MigrationDocumentsE8ReleaseGateMigrationInstructions)$$' -count=1

RELEASE_WITH_GATE ?= 0

release-v2.4.0:
	@if [ -n "$$(git status --short)" ]; then \
		echo "release preflight failed: working tree is not clean"; \
		git status --short; \
		exit 1; \
	fi
	go test ./...
	go build ./...
	@if [ "$(RELEASE_WITH_GATE)" = "1" ]; then make release-gate-e8; fi
	@echo "Tagging:"
	@echo "git tag -a v2.4.0 -m \"Release v2.4.0\""
	@echo "git push origin v2.4.0"

build:
	mkdir -p bin
	go build -o bin/yolo-runner ./cmd/yolo-runner
	go build -o bin/yolo-agent ./cmd/yolo-agent
	go build -o bin/yolo-task ./cmd/yolo-task
	go build -o bin/yolo-tui ./cmd/yolo-tui
	go build -o bin/yolo-linear-webhook ./cmd/yolo-linear-webhook
	go build -o bin/yolo-linear-worker ./cmd/yolo-linear-worker

PREFIX ?= /usr/local

install: build
	mkdir -p $(PREFIX)/bin
	cp bin/yolo-runner $(PREFIX)/bin/yolo-runner
	cp bin/yolo-agent $(PREFIX)/bin/yolo-agent
	cp bin/yolo-task $(PREFIX)/bin/yolo-task
	cp bin/yolo-tui $(PREFIX)/bin/yolo-tui
	cp bin/yolo-linear-webhook $(PREFIX)/bin/yolo-linear-webhook
	cp bin/yolo-linear-worker $(PREFIX)/bin/yolo-linear-worker
	chmod 755 $(PREFIX)/bin/yolo-runner $(PREFIX)/bin/yolo-agent $(PREFIX)/bin/yolo-task $(PREFIX)/bin/yolo-tui $(PREFIX)/bin/yolo-linear-webhook $(PREFIX)/bin/yolo-linear-worker
