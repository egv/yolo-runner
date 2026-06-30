#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
find_repo_root() {
  local dir="$script_dir"
  while [[ "$dir" != "/" ]]; do
    if [[ -x "$dir/bin/yolo-agent" && -x "$dir/bin/yolo-tui" ]]; then
      printf '%s\n' "$dir"
      return 0
    fi
    dir="$(dirname "$dir")"
  done
  return 1
}

repo_root="$(find_repo_root)" || {
  echo "could not find repo root with executable bin/yolo-agent and bin/yolo-tui above $script_dir" >&2
  exit 1
}
yolo_agent="$repo_root/bin/yolo-agent"
yolo_tui="$repo_root/bin/yolo-tui"

repo="$repo_root"
events="/tmp/yolo-board.events.jsonl"
tui_snapshot="/tmp/yolo-board.tui.txt"
stream_log="/tmp/yolo-board.stream.jsonl"
watch_stderr="/tmp/yolo-board.watch.stderr.log"
environments="${HOME}/.yolo-runner/environments.yaml"
run_watch=0
fixture=0
duration=90

usage() {
  cat <<'EOF'
Usage: scripts/verify-f5b-dogfood-rerun.sh [--run] [options]

Verifies F5b dogfood evidence:
  - agent_blocked with reason permission_denied is present in the NDJSON stream
  - source_poll and runner_alive fleet events are present
  - yolo-tui renders the blocked event and reason from the captured stream

Options:
  --run                    Launch yolo-agent watch before verification.
  --fixture                With --run, build an isolated local dogfood fixture
                           that drives a real queue runner through the Claude
                           permission-denied detector without external tokens.
  --repo PATH              Repository root passed to yolo-agent watch.
  --events PATH            Event log path. Default: /tmp/yolo-board.events.jsonl
  --tui-snapshot PATH      TUI snapshot path. Default: /tmp/yolo-board.tui.txt
  --stream-log PATH        Raw --stream stdout path. Default: /tmp/yolo-board.stream.jsonl
  --watch-stderr PATH      yolo-agent watch stderr path. Default: /tmp/yolo-board.watch.stderr.log
  --environments PATH      Environment presets path. Default: ~/.yolo-runner/environments.yaml
  --duration SECONDS       Bounded watch duration for --run. Default: 90
  -h, --help               Show this help.

Required env for --run depends on .yolo-runner/config.yaml, typically:
  STARTREK_TOKEN and ARC_TOKEN for queue-backed Startrek/Arc PR operation.

The script resolves yolo-agent and yolo-tui from the repository root that
contains this script, so it may be launched from any working directory.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --run)
      run_watch=1
      shift
      ;;
    --fixture)
      fixture=1
      shift
      ;;
    --repo)
      repo="${2:?--repo requires a path}"
      shift 2
      ;;
    --events)
      events="${2:?--events requires a path}"
      shift 2
      ;;
    --tui-snapshot)
      tui_snapshot="${2:?--tui-snapshot requires a path}"
      shift 2
      ;;
    --stream-log)
      stream_log="${2:?--stream-log requires a path}"
      shift 2
      ;;
    --watch-stderr)
      watch_stderr="${2:?--watch-stderr requires a path}"
      shift 2
      ;;
    --environments)
      environments="${2:?--environments requires a path}"
      shift 2
      ;;
    --duration)
      duration="${2:?--duration requires seconds}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_file() {
  local path="$1"
  if [[ ! -s "$path" ]]; then
    echo "missing or empty file: $path" >&2
    exit 1
  fi
}

require_match() {
  local pattern="$1"
  local path="$2"
  local label="$3"
  if ! grep -Eq "$pattern" "$path"; then
    echo "missing $label in $path" >&2
    exit 1
  fi
}

require_json_event_match() {
  local path="$1"
  local label="$2"
  local expression="$3"
  if ! python3 - "$path" "$expression" <<'PY'
import json
import sys

path = sys.argv[1]
expression = sys.argv[2]

def matches(event):
    if expression == "agent_blocked_permission_denied":
        return event.get("type") == "agent_blocked" and event.get("reason") == "permission_denied"
    raise SystemExit(f"unknown expression: {expression}")

with open(path, encoding="utf-8") as fh:
    for line in fh:
        line = line.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if matches(event):
            sys.exit(0)
sys.exit(1)
PY
  then
    echo "missing $label in $path" >&2
    exit 1
  fi
}

setup_fixture() {
  local root="$1"
  local fixture_root="$root/f5b-fixture"
  local source_repo="$fixture_root/source-repo"
  local dogfood_repo="$fixture_root/watch-repo"
  local fake_bin="$fixture_root/bin"
  local queue="$fixture_root/watch.db"
  local env_file="$fixture_root/environments.yaml"
  local now

  rm -rf "$fixture_root"
  mkdir -p "$source_repo" "$dogfood_repo/.yolo-runner" "$fake_bin"

  git -C "$source_repo" init -q
  git -C "$source_repo" config user.email "f5b-dogfood@example.invalid"
  git -C "$source_repo" config user.name "F5b Dogfood"
  printf 'f5b dogfood\n' > "$source_repo/README.md"
  git -C "$source_repo" add README.md
  git -C "$source_repo" commit -q -m "initial"
  git -C "$source_repo" branch -M main
  mkdir -p "$source_repo/.beads"

cat > "$fake_bin/claude" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' '{"type":"system","subtype":"init"}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_f5b","name":"Bash","input":{"command":"git status"}}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_f5b","is_error":true,"content":[{"type":"text","text":"Claude requested permissions for Bash, but you haven'\''t granted them."}]}]}}'
printf '%s\n' '{"type":"result","subtype":"success"}'
EOF
  chmod 755 "$fake_bin/claude"

  cat > "$env_file" <<EOF
presets:
  f5b-dogfood:
    workspace:
      strategy: git-clone
      origin: "$source_repo"
      base_branch: main
    landing:
      type: none
    agent:
      backend: claude
      model: f5b-fixture
      runner_timeout: 20s
      watchdog_timeout: 10s
      watchdog_interval: 1s
    limits:
      max_concurrent: 1
EOF

  cat > "$dogfood_repo/.yolo-runner/config.yaml" <<EOF
default_profile: beads
profiles:
  beads:
    tracker:
      type: beads
watch:
  queue_path: "$queue"
  sources:
    - name: f5b-local
      type: br
      repo: "$source_repo"
      preset: f5b-dogfood
  runner_pools:
    - name: f5b-local-runners
      source: f5b-local
      presets: [f5b-dogfood]
      min_replicas: 1
      max_replicas: 1
      capacity: 1
  autoscale:
    min_runners: 1
    max_runners: 1
  tui:
    default_mode: stream
EOF

  "$yolo_agent" runner \
    --queue "$queue" \
    --environments "$env_file" \
    --presets f5b-dogfood \
    --runner-id f5b-schema-init \
    --once >/dev/null

  now="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  sqlite3 "$queue" <<EOF
INSERT INTO work_items (
  id, kind, source, source_ref, idempotency_key, preset, priority, payload,
  state, attempt, max_attempts, not_before, claimed_by, lease_expires_at,
  heartbeat_at, created_at, updated_at
) VALUES (
  'f5b-dogfood-item', 'implement', 'f5b-local', 'F5B-DOGFOOD',
  'f5b-dogfood/permission-denied', 'f5b-dogfood', 10,
  '{"task_id":"F5B-DOGFOOD","title":"F5b permission denied fixture","description":"Run the dogfood permission-denied path and report structured blocking.","prompt_context":{"parent_id":"","metadata":{"dogfood":"f5b","fixture":"permission_denied"}}}',
  'pending', 0, 1, '', '', '', '', '$now', '$now'
);
EOF

  repo="$dogfood_repo"
  environments="$env_file"
  export PATH="$fake_bin:$PATH"
}

if [[ "$run_watch" -eq 1 ]]; then
  mkdir -p "$(dirname "$events")" "$(dirname "$tui_snapshot")" "$(dirname "$stream_log")" "$(dirname "$watch_stderr")"
  : > "$events"
  : > "$stream_log"
  : > "$watch_stderr"

  if [[ "$fixture" -eq 1 ]]; then
    setup_fixture "$(dirname "$events")"
  fi

  "$yolo_agent" config validate --repo "$repo"

  "$yolo_agent" watch \
    --repo "$repo" \
    --environments "$environments" \
    --events "$events" \
    --stream > "$stream_log" 2> "$watch_stderr" &
  watch_pid=$!

  elapsed=0
  while kill -0 "$watch_pid" 2>/dev/null && [[ "$elapsed" -lt "$duration" ]]; do
    sleep 1
    elapsed=$((elapsed + 1))
  done
  kill "$watch_pid" 2>/dev/null || true
  wait "$watch_pid" 2>/dev/null || true
fi

require_file "$events"
require_json_event_match "$events" "agent_blocked event with permission_denied reason" "agent_blocked_permission_denied"
require_match '"type"[[:space:]]*:[[:space:]]*"source_poll"' "$events" "source_poll fleet event"
require_match '"type"[[:space:]]*:[[:space:]]*"runner_alive"' "$events" "runner_alive fleet event"

"$yolo_tui" --events-stdin < "$events" > "$tui_snapshot"

require_file "$tui_snapshot"
require_match 'agent_blocked' "$tui_snapshot" "agent_blocked TUI row"
require_match 'permission_denied' "$tui_snapshot" "permission_denied TUI detail"

echo "F5b dogfood verification passed"
echo "events=$events"
echo "tui_snapshot=$tui_snapshot"
