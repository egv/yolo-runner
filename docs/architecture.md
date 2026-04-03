# Architecture

This diagram reflects the current detailed runtime architecture.

```mermaid
flowchart TB
  User["Developer / CI"]
  GitRepo[(Git repository)]
  RepoConfig[".yolo-runner/config.yaml"]
  CloneRoots[".yolo-runner/clones/*"]
  EventLogs["runner-logs/*.events.jsonl"]
  TicketFiles[".tickets/ markdown"]
  BeadsState[".beads / br storage"]
  GitHubAPI[(GitHub API)]
  LinearAPI[(Linear API)]
  BusInfra[(Redis / NATS)]
  AgentProcesses["External agent CLIs<br/>OpenCode / Codex / Claude / Kimi / Gemini"]

  subgraph CLIs["CLI Entry Points"]
    YoloAgent["cmd/yolo-agent"]
    YoloTUI["cmd/yolo-tui"]
  end

  subgraph Shared["Shared Contracts"]
    Contracts["internal/contracts"]
  end

  subgraph AgentRuntime["yolo-agent Runtime"]
    ProfileResolver["tracker profile + config resolver"]
    Catalog["internal/codingagents<br/>builtin + custom backend catalog"]
    LocalLoop["local role<br/>internal/agent.Loop"]
    StorageMgr["storageEngineTaskManager"]
    Engine["internal/engine<br/>task graph + concurrency"]
    Quality["internal/task_quality"]
    CloneManager["Git clone manager"]
    VCS["internal/vcs/git"]
    Executor["executor role<br/>internal/distributed.ExecutorWorker"]
    Bus["internal/distributed.Bus"]
  end

  subgraph Storage["Tracker / Storage Backends"]
    TK["internal/tk"]
    GitHubBackend["internal/github"]
    LinearBackend["internal/linear"]
    Beads["internal/beads"]
  end

  subgraph Runners["Agent Runner Adapters"]
    OpenCode["internal/opencode"]
    Codex["internal/codex"]
    Claude["internal/claude"]
    Kimi["internal/kimi"]
    GenericCmd["generic command adapter"]
  end

  subgraph Monitoring["Monitoring / UI State"]
    MonitorModel["internal/ui/monitor"]
  end

  User --> YoloAgent
  User --> YoloTUI

  YoloAgent --> RepoConfig
  YoloAgent --> ProfileResolver
  YoloAgent --> Catalog
  YoloAgent -- role=local --> LocalLoop
  YoloAgent -- role=executor --> Executor
  YoloAgent -- mode=ui launches --> YoloTUI
  YoloAgent -- stream/file sinks --> EventLogs
  YoloAgent -- monitor sink --> Bus

  ProfileResolver --> TK
  ProfileResolver --> GitHubBackend
  ProfileResolver --> LinearBackend
  ProfileResolver --> Beads

  LocalLoop --> StorageMgr
  LocalLoop --> Quality
  LocalLoop --> CloneManager
  LocalLoop --> VCS
  LocalLoop --> Catalog

  StorageMgr --> Engine
  StorageMgr --> TK
  StorageMgr --> GitHubBackend
  StorageMgr --> LinearBackend
  StorageMgr --> Beads

  Catalog --> OpenCode
  Catalog --> Codex
  Catalog --> Claude
  Catalog --> Kimi
  Catalog --> GenericCmd

  OpenCode --> AgentProcesses
  Codex --> AgentProcesses
  Claude --> AgentProcesses
  Kimi --> AgentProcesses
  GenericCmd --> AgentProcesses

  Executor --> Bus
  Executor --> Catalog

  Bus -- backend --> BusInfra

  YoloTUI --> MonitorModel
  EventLogs -- stdin/file --> YoloTUI
  Bus -- monitor events --> YoloTUI

  TK -- local tickets --> TicketFiles
  Beads -- CLI / state --> BeadsState
  GitHubBackend -- API --> GitHubAPI
  LinearBackend -- API --> LinearAPI
  CloneManager --> CloneRoots
  CloneManager --> GitRepo
  VCS --> GitRepo

  LocalLoop --> Contracts
  StorageMgr --> Contracts
  Engine --> Contracts
  Executor --> Contracts
  TK --> Contracts
  GitHubBackend --> Contracts
  LinearBackend --> Contracts
  Beads --> Contracts
  OpenCode --> Contracts
  Codex --> Contracts
  Claude --> Contracts
  Kimi --> Contracts
  MonitorModel --> Contracts
  Bus --> Contracts
```

## Task Storage Flow

The storage layer hides tracker-specific details behind a shared task contract so the scheduler can reason about one normalized graph.

```mermaid
flowchart LR
  User["Developer / CI"] --> Tracker["TK / GitHub / Linear / beads"]
  Tracker --> Adapter["backend adapter"]
  Adapter --> Contracts["internal/contracts task model"]
  Contracts --> StorageMgr["storageEngineTaskManager"]
  StorageMgr --> Graph["task graph + dependency state"]
  Graph --> Ready["runnable task set"]
  StorageMgr --> Updates["status / dependency / parent updates"]
  Updates --> Adapter --> Tracker
```

## Local Orchestrator Loop

The local `yolo-agent` process owns the scheduling loop: it keeps reloading state, launching runners, and folding results back into the tracker.

```mermaid
flowchart TB
  Start["yolo-agent --role local"] --> Resolve["resolve profile, repo config, backend"]
  Resolve --> Load["load task tree and current statuses"]
  Load --> Pick["compute runnable tasks and free slots"]
  Pick --> Launch["start runner for each selected task"]
  Launch --> Monitor["collect logs, events, heartbeats, test results"]
  Monitor --> Review["apply review, quality, retry, rewrite policy"]
  Review --> Update["merge results and update tracker state"]
  Update --> Load
```

## Runner Execution Lifecycle

Each runner turns a scheduler assignment into concrete work in an isolated clone, while keeping agent-specific details behind the backend catalog.

```mermaid
flowchart TB
  Assigned["task assignment"] --> Clone["create or reuse task clone"]
  Clone --> Brief["materialize task brief, profile, env"]
  Brief --> Catalog["backend catalog"]
  Catalog --> Runner["OpenCode / Codex / Claude / Kimi / generic adapter"]
  Runner --> Agent["external coding agent CLI"]
  Agent --> Work["edit code, run tests, write commits, emit artifacts"]
  Work --> Report["final status, events, logs"]
  Report --> Caller["local loop or executor caller"]
```

## Review Guardrails

The review stage is intentionally layered: deterministic checks run first, LLM judgement runs second, and tracker state changes happen last.

```mermaid
flowchart LR
  Result["runner result"] --> Tests["targeted checks"]
  Tests --> Diff["diff + changed files"]
  Diff --> Review["review pass"]
  Review --> Policy["guardrails / policy"]
  Policy --> Gate{"accept?"}
  Gate -->|yes| Merge["merge + close task"]
  Gate -->|no| Retry["retry / rewrite / comment"]
  Retry --> Result
```

## Where Prompting Lives

The system deliberately keeps task state, retries, and status transitions in code. Prompts are used only where model judgement is actually valuable: implementation and review.

```mermaid
flowchart LR
  CodeA["code: statuses, dependencies, retries"] --> PromptA["LLM prompt: task execution"]
  PromptA --> CodeB["code: clone, git, events, tests"]
  CodeB --> PromptB["LLM prompt: review and risk surfacing"]
  PromptB --> CodeC["code: merge / close / reopen"]
```

## Monitoring And Event Flow

Monitoring is intentionally decoupled from task execution: the UI can follow either local JSONL event files or the distributed event bus.

```mermaid
flowchart LR
  LocalLoop["local loop"] --> FileEvents["runner-logs/*.events.jsonl"]
  LocalLoop --> Bus["distributed bus"]
  Executor["executor worker"] --> Bus
  FileEvents --> TUI["yolo-tui"]
  Bus --> TUI
  TUI --> Monitor["monitor model"]
```

## Distributed Executor Mode

Remote executors keep the same runner contracts as the local process; only task dispatch and event transport move onto the bus.

```mermaid
flowchart LR
  LocalAgent["yolo-agent --role local"] --> Storage["tracker backend"]
  LocalAgent --> Bus["Redis / NATS bus"]
  Bus --> RemoteExec["yolo-agent --role executor"]
  RemoteExec --> Catalog["backend catalog"]
  RemoteExec --> Clone["task clone"]
  Catalog --> AgentCLI["agent CLI"]
  Clone --> AgentCLI
  AgentCLI --> Bus
```
