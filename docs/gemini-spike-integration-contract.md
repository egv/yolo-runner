# Gemini CLI Integration Contract (Task #161)

## Non-interactive execution

Gemini CLI runs in a headless, one-shot mode whenever it is not attached to a TTY or when invoked with a query.

- Use `gemini -p "<prompt>"` to execute and exit.
- Use `gemini "<query>"` as an equivalent positional one-shot form.
- Pipe input with `cat README.md | gemini` when prompt + stdin are both provided.

## Streaming progress/events

The CLI supports `--output-format` to control event stream shape in headless mode:

- `--output-format json` returns a single JSON object.
- `--output-format stream-json` returns newline-delimited JSON events.

Observed stream event types from the Gemini docs:

- `init`
- `message`
- `tool_use`
- `tool_result`
- `error`
- `result`

## Final result extraction

For non-stream output (`--output-format json`): read the `response` field from the JSON object.

For stream output (`--output-format stream-json`): read the terminal `result` event as completion.

## ACP status

Gemini CLI exposes an experimental ACP entrypoint:

- Enable with `--experimental-acp` (from CLI flags reference).
- Evidence from logs shows ACP bootstrap JSON-RPC `initialize` responses containing `protocolVersion: 1`, therefore we must target protocol version **1**.
- `authMethods` reported during initialize include `oauth-personal`, `gemini-api-key`, and `vertex-ai`.
- Example reported auth constraints indicate ACP is intended to run as a spawned process (`gemini --experimental-acp`) rather than a separately managed always-on server command.
- Reported MCP forwarding behavior in ACP indicates `session/new` currently expects command-style MCP server fields, which implies practical ACP transport alignment with stdio tool wiring (not SSE/HTTP MCP forwarding).

If ACP support is treated as not production-stable, enforce:

- start via command/stdio process mode only
- do not assume long-running HTTP server semantics

## Env vars

Required env vars and auth-related settings:

- API key mode: `GEMINI_API_KEY`
- Alternate key mode: `GOOGLE_API_KEY`
- Vertex AI mode: `GOOGLE_API_KEY` plus `GOOGLE_GENAI_USE_VERTEXAI=true`
- Vertex project/location: `GOOGLE_CLOUD_PROJECT`, `GOOGLE_CLOUD_LOCATION`
- Vertex ADC/service account: `GOOGLE_APPLICATION_CREDENTIALS`

Persisted env loading path used by Gemini CLI docs:

- project-local `.gemini/.env`
- fallback to `~/.gemini/.env` or `~/.env`

## Event mapping

Proposed yolo-runner mapping from Gemini stream output (aligned to existing event contract):

- `init` -> `runner_progress`
- `message` -> `runner_output`
- `message` thought/reasoning payloads -> `runner_progress` with `metadata["phase"]=thought`
- `tool_use` -> `runner_progress` with `metadata["phase"]=tool_call`
- `tool_result` -> `runner_output` with `metadata["phase"]=tool_result`
- `error` -> `runner_warning`
- `result` -> emit final `runner_output` containing `result` payload and follow with
  `runner_finished` metadata (`result: completed`, `reason: result`) for completion.
- `runner_progress` metadata should include a `reason` field for lifecycle transitions like `init`, `tool_call`, `tool_result`, and `thought`.

There is no `runner_tool_call` or `runner_completion` event in the current yolo-runner schema, so implementers must not emit those types directly.

## Smoke task

Minimal hello harness

```bash
gemini -p "Say hello in one short sentence." --output-format json
```

Expected outputs:

- First command exits with status 0 and prints a JSON object where `response` includes a greeting.
- Non-stream mode output must include at least one JSON field containing the greeting text in the top-level `response`.

Deterministic ACP smoke check

```bash
set -o pipefail
timeout 10s sh -c 'gemini --experimental-acp --output-format stream-json' |
  tee /tmp/gemini-acp-smoke.log
```

Expected behavior:

- The command should not block forever; `timeout` must terminate the process.
- The stream should contain an ACP init payload with `protocolVersion` (target `1`) and `method`/`id` fields for JSON-RPC initialize cycle.
- If auth is non-interactive, `GEMINI_API_KEY` or `GOOGLE_API_KEY` (+ optional `GOOGLE_GENAI_USE_VERTEXAI`) must be set in environment.
