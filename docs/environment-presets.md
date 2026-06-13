# Environment Presets

Runner processes are project-agnostic daemons. They learn *where a project lives*
and *how to land its work* from named **environment presets** in a global file,
`~/.yolo-runner/environments.yaml`. Work items carry only the preset **name**;
the runner resolves the preset at claim time. Secrets never enter the queue.

A ready-to-copy example lives at [`docs/environments.example.yaml`](environments.example.yaml).

## File location

`~/.yolo-runner/environments.yaml` (override per-runner with
`yolo-agent runner --environments <path>`).

## Schema

```yaml
presets:
  <preset-name>:
    workspace:
      strategy: git-clone | arc-shared | path
      # git-clone:
      origin: <path-or-url>        # clone source; the clone's remote is rewritten to its origin for push
      base_branch: main            # checked out and fast-forwarded before code-writing work
      # arc-shared:
      mount: ~/arcadia             # arc mount point
      subpath: path/within/mount   # project subdirectory
      # path:
      path: /abs/path              # read-in-place (model-only kinds)
    landing:
      type: git-merge | arc-pr | none
      title_template: "Land {{ .TaskID }}: {{ .TaskTitle }}"   # optional
    agent:
      backend: codex | claude | opencode | opencode-serve | opencode-acp | codex-cli | kimi | gemini
      model: <model-id>
      runner_timeout: 45m          # optional
      watchdog_timeout: 10m        # optional
      watchdog_interval: 5s        # optional
    limits:
      max_concurrent: 1            # per-preset in-flight cap (use 1 for arc-shared)
    env:
      passthrough: [STARTREK_TOKEN, ARC_TOKEN]   # allow-list from the runner environment
      set: { YOLO_PROJECT: adapta }              # extra vars
```

## Workspace strategies

- **`git-clone`** — a fresh isolated clone per work item under
  `/tmp/yolo-runner-clones/<item>`. The clone's `origin` is rewritten to the
  source repo's origin so landing pushes to the real remote. For code-writing
  kinds the clone is fast-forwarded to `base_branch` before work. This is the
  default for git projects and matches the old loop's clone-per-task isolation.
- **`arc-shared`** — a shared arc mount with a **per-item branch** for
  code-writing kinds (serialized by a per-mount lock — set `max_concurrent: 1`),
  and a **mount-only read view with no branch and no lock** for read-only kinds
  (so PR reviews and preflights run in parallel).
- **`path`** — run in place, read-only. Valid only for model-only kinds
  (`preflight`, `split`, `pr-review`); code-writing kinds (`implement`,
  `finalize`) are rejected if they would have no isolated VCS workspace.

## Landing policies

- **`git-merge`** — merge the per-item branch to `main` and push (serialized by
  the landing lock).
- **`arc-pr`** — create an Arcanum PR (deferred landing).
- **`none`** — leave the branch and report it in the result; the source decides
  what to do.

## Worked examples

Git project (this repo), run with a standalone runner:

```yaml
presets:
  yolo-runner:
    workspace: { strategy: git-clone, origin: ~/yolo-runner, base_branch: main }
    landing:   { type: git-merge }
    agent:     { backend: codex, model: gpt-5.5 }
    limits:    { max_concurrent: 2 }
```

```bash
yolo-agent runner --queue ~/.yolo-runner/queue.db \
  --environments ~/.yolo-runner/environments.yaml --presets yolo-runner
# producer (preset name must match the tracker profile name):
yolo-agent --repo ~/yolo-runner --root <epic-id> --profile yolo-runner \
  --queue ~/.yolo-runner/queue.db
```

Arc project (adapta), shared mount:

```yaml
presets:
  adapta:
    workspace: { strategy: arc-shared, mount: ~/arcadia, subpath: marvel/gena/adapta }
    landing:   { type: arc-pr, title_template: "Land {{ .TaskID }}: {{ .TaskTitle }}" }
    agent:     { backend: codex, model: gpt-5.5, runner_timeout: 20m }
    limits:    { max_concurrent: 1 }
    env:       { passthrough: [STARTREK_TOKEN, ARC_TOKEN] }
```

## Embedded preset (single-command convenience)

`yolo-agent --repo . --root <id> --queue <db>` (without a standalone runner)
synthesizes a `git-clone` preset from the repo automatically and starts an
embedded runner — so single-command runs still isolate. **Arc repos are not
supported by the embedded path**: define an explicit `arc-shared` preset and run
a standalone `runner`.

## Notes

- The preset name a producer submits equals its `--profile` (tracker profile)
  name. Define a preset of the same name in `environments.yaml`.
- Preset definitions are resolved at claim time, so editing `environments.yaml`
  affects already-queued items on their next claim (useful for hotfixing a bad
  model choice); the resolved preset is recorded in the result for audit.
