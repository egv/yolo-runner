# Config Workflow Runbook

## Command Usage

Initialize a starter config:

```bash
./bin/yolo-agent config init --repo .
```

Overwrite an existing config intentionally:

```bash
./bin/yolo-agent config init --repo . --force
```

Validate current config in human-readable mode:

```bash
./bin/yolo-agent config validate --repo .
```

Validate current config in JSON mode:

```bash
./bin/yolo-agent config validate --repo . --format json
```

## Precedence

`yolo-agent config validate` resolves only profile/root selection at runtime:

- Profile: `--profile > YOLO_PROFILE > default_profile > default`
- Root scope for tracker validation: `--root > profiles.<selected>.tracker.tk.scope.root (when tracker.type=tk) > empty`
- Backend and other `agent.*` values are validated from `.yolo-runner/config.yaml` as written.
- `--agent-backend` and `--backend` are not supported by `config validate`; passing either flag fails with `flag provided but not defined`.
- `YOLO_AGENT_BACKEND` is not read by `config validate`.

## Common Failures

- `config file at .yolo-runner/config.yaml already exists; rerun with --force to overwrite`
- `flag provided but not defined: -backend` (same for `-agent-backend`)
- `unsupported --format value "<value>" (supported: text, json)`
- `tracker profile "<name>" not found (available: ...)`
- `missing auth token from <ENV_VAR>`
- `config is invalid` with a `field:` / `reason:` / `remediation:` block

## Remediation

1. Run `./bin/yolo-agent config init --repo .` to generate a known-good starter file.
2. If the file exists and replacement is intentional, rerun with `--force`.
3. Run `./bin/yolo-agent config validate --repo .` and fix the reported `field` using the included remediation text.
4. For token failures (`missing auth token from <ENV_VAR>`), export the named variable in your shell, then rerun validation.
5. For profile failures, set `default_profile` to a key that exists under `profiles`, or pass `--profile` explicitly.
6. For `flag provided but not defined` on `-backend`/`-agent-backend`, remove those flags from `config validate` and set `agent.backend` in `.yolo-runner/config.yaml`.
