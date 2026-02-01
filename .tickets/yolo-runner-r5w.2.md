---
id: yolo-runner-r5w.2
status: closed
deps: []
links: []
created: 2026-01-18T21:46:18.490678+03:00
type: task
priority: 0
parent: yolo-runner-r5w.14
---
# v1: Initialize Go module + CLI flags

Create Go module and a minimal yolo-runner CLI that parses flags with correct defaults.

Files:
- Create/Modify: go.mod
- Create: cmd/yolo-runner/main.go
- Create: cmd/yolo-runner/main_test.go

Flags:
- --repo (default .)
- --root (default algi-8bt)
- --max (optional)
- --dry-run
- --model (optional)

Rules:
- This task must be implemented in Go
- Agent name is fixed to yolo (no --agent flag)

Acceptance:
- go test ./... passes
- go build ./... succeeds
- parseArgs defaults: repo=., root=algi-8bt, max=0/unset, dry_run=false, model=""

## Acceptance Criteria

- Given no flags, when running `yolo-runner --help`, then it documents flags: --repo, --root, --max, --dry-run, --model
- Given args=[], when parseArgs runs, then defaults are repo=".", root="algi-8bt", max=0 (or unset), dry_run=false, model=""
- Given `go test ./...`, when run, then it fails before implementation and passes after implementation
- Given `go build ./...`, when run, then it succeeds


