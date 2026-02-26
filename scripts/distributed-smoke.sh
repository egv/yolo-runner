#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="${REPO_ROOT}/dev/distributed/docker-compose.yml"

REDIS_PORT="${YOLO_SMOKE_REDIS_PORT:-16379}"
NATS_PORT="${YOLO_SMOKE_NATS_PORT:-14222}"
EVENTS_DIR="${YOLO_DISTRIBUTED_SMOKE_EVENTS_DIR:-${REPO_ROOT}/runner-logs/distributed-smoke}"

mkdir -p "${EVENTS_DIR}"

COMPOSE_CMD=()
if docker compose version >/dev/null 2>&1; then
  COMPOSE_CMD=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE_CMD=(docker-compose)
else
  echo "docker compose or docker-compose is required for distributed smoke harness" >&2
  exit 1
fi

cleanup() {
  "${COMPOSE_CMD[@]}" -f "${COMPOSE_FILE}" down -v >/dev/null 2>&1 || true
}

if [[ "${YOLO_DISTRIBUTED_SMOKE_KEEP_UP:-0}" != "1" ]]; then
  trap cleanup EXIT
fi

"${COMPOSE_CMD[@]}" -f "${COMPOSE_FILE}" up -d redis nats

wait_for_port() {
  local host="$1"
  local port="$2"
  local label="$3"
  for _ in $(seq 1 60); do
    if (echo >/dev/tcp/"${host}"/"${port}") >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "${label} did not become ready at ${host}:${port}" >&2
  return 1
}

wait_for_port "127.0.0.1" "${REDIS_PORT}" "redis"
wait_for_port "127.0.0.1" "${NATS_PORT}" "nats"

YOLO_DISTRIBUTED_SMOKE_REDIS_ADDR="redis://127.0.0.1:${REDIS_PORT}" \
YOLO_DISTRIBUTED_SMOKE_NATS_ADDR="nats://127.0.0.1:${NATS_PORT}" \
YOLO_DISTRIBUTED_SMOKE_EVENTS_DIR="${EVENTS_DIR}" \
go test ./internal/distributed -run '^TestDistributedE2ESmokeHarness$' -count=1 -v

echo "distributed smoke events written to ${EVENTS_DIR}"
