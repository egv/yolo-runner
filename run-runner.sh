#!/usr/bin/env bash
set -euo pipefail

export BEADS_NO_DAEMON=1

# Run from repo root so OpenCode can discover .opencode/agent/yolo.md
REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$REPO_ROOT"

mkdir -p runner-logs

# If you want to pin a model for OpenCode, set it here and update beads_yolo_runner.py to forward it.
# export OPENCODE_MODEL="openai/gpt-5.2-codex"

uv sync

uv run python beads_yolo_runner.py \
  --repo . \
  --root yolo-runner-r5w \
  2>&1 | tee runner-logs/beads_yolo_runner.run.log
