package docs

import (
	"strings"
	"testing"
)

func TestReadmeDocumentsQueueSplitOperatorTopology(t *testing.T) {
	readme := readRepoFile(t, "README.md")

	required := []string{
		"### Queue-split runner topology",
		"`yolo-agent source <arcpr|startrek> --profile <name> --queue ~/.yolo-runner/queue.db`",
		"`yolo-agent runner --queue ~/.yolo-runner/queue.db --environments ~/.yolo-runner/environments.yaml --presets <preset>[,<preset>]`",
		"a `yolo-agent queue` operator CLI (`ls`/`submit`/`retry`/`cancel`/`gc`) is planned",
		"`yolo-agent events follow --since 1h | yolo-tui --events-stdin`",
		"`~/.yolo-runner/environments.yaml`",
		"`tracker-watch` and `arc-review-watch` are deprecated compatibility shims",
	}
	for _, needle := range required {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README missing queue-split operator guidance: %q", needle)
		}
	}
}

func TestReadmeDocumentsWatchConfigurationExample(t *testing.T) {
	readme := readRepoFile(t, "README.md")

	required := []string{
		"### Watch configuration",
		"`yolo-agent watch` starts configured sources and autoscaled runner pools from one command",
		"type: startrek",
		"queues:",
		"type: arcpr",
		"runner_pools:",
		"autoscale:",
		"default_mode: stream",
		"limits:",
		"max_concurrent: 1",
		"export STARTREK_TOKEN=<startrek-api-token>",
		"export ARC_TOKEN=<arc-token>",
		"./bin/yolo-agent watch --repo . --environments ~/.yolo-runner/environments.yaml --tui",
	}
	for _, needle := range required {
		if !strings.Contains(readme, needle) {
			t.Fatalf("README missing watch configuration guidance: %q", needle)
		}
	}
}
