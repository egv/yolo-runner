---
id: yolo-runner-127.4.9.1
status: closed
deps: []
links: []
created: 2026-01-19T18:40:25.625097+03:00
type: task
priority: 0
parent: yolo-runner-127.4.9
---
# Align: Default isolated OpenCode config (XDG_CONFIG_HOME)

Make Go runner use the same isolated OpenCode config layout as beads_yolo_runner.py by default.

Why:
- Python runner sets XDG_CONFIG_HOME=~/.config/opencode-runner to avoid user global config and avoid interactive permission prompts.
- Go runner currently passes empty configRoot/configDir so OpenCode uses global config.

What:
- In cmd/yolo-runner/main.go, set RunOnceOptions.ConfigRoot and ConfigDir defaults to match Python:
  - ConfigRoot: $HOME/.config/opencode-runner
  - ConfigDir:  $ConfigRoot/opencode
- Add flags to override if needed later (optional), but default must be isolated.

Files:
- Modify: cmd/yolo-runner/main.go
- Modify: cmd/yolo-runner/main_test.go
- Modify (if needed): internal/opencode/client.go

Acceptance:
- Given no explicit config flags, when building env for opencode, then XDG_CONFIG_HOME is set and OPENCODE_CONFIG_DIR/OPENCODE_CONFIG are set
- Given no explicit config flags, opencode.json is created if missing
- go test ./... passes

## Acceptance Criteria

- Default ConfigRoot/ConfigDir are set to ~/.config/opencode-runner and ~/.config/opencode-runner/opencode
- Env includes XDG_CONFIG_HOME and OPENCODE_CONFIG* vars
- opencode.json created if missing
- go test ./... passes


