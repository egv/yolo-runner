test:
	go test ./...

smoke-agent-tui:
	go test ./cmd/yolo-agent ./cmd/yolo-tui
	$(MAKE) smoke-config-commands

smoke-config-commands:
	go test ./cmd/yolo-agent -run 'TestE2E_ConfigCommands_(InitThenValidateHappyPath|ValidateMissingFileFallsBackToDefaults|ValidateInvalidValuesReportsDeterministicDiagnostics|ValidateMissingAuthEnvReportsRemediation)$$' -count=1

smoke-event-stream:
	$(MAKE) smoke-agent-tui

release-gate-e8:
	go test ./cmd/yolo-agent -run 'TestE2E_(CodexTKConcurrency2LandsViaMergeQueue|ClaudeConflictRetryPathFinalizesWithLandingOrBlockedTriage|KimiLinearProfileProcessesAndClosesIssue|GitHubProfileProcessesAndClosesIssue)$$' -count=1
	go test ./internal/docs -run 'Test(MakefileDefinesE8ReleaseGateChecklistTarget|ReadmeDocumentsE8ReleaseGateChecklist|MigrationDocumentsE8ReleaseGateMigrationInstructions)$$' -count=1

build:
	mkdir -p bin
	go build -o bin/yolo-runner ./cmd/yolo-runner
	go build -o bin/yolo-agent ./cmd/yolo-agent
	go build -o bin/yolo-task ./cmd/yolo-task
	go build -o bin/yolo-tui ./cmd/yolo-tui
	go build -o bin/yolo-linear-webhook ./cmd/yolo-linear-webhook
