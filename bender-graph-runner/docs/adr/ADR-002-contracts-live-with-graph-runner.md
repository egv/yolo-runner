# ADR-002: Shared Contracts Live with the Workflow Engine Repo

Status: Proposed
Date: 2026-03-05
Codename: BENDER

## Context
BENDER is split into multiple repositories:
- Task Manager
- Mastermind + Runner
- Graph Runner

RPC APIs and event schemas must not drift. We need one authoritative location for proto definitions and event contracts.

## Decision
- Host `bender-contracts` (proto files + generated Rust crate) in `bender-graph-runner`.
- Other repos depend on `bender-contracts` by version/tag.

## Consequences
### Positive
- Single source of truth for RPC and event schema.
- Contracts evolve alongside workflow execution semantics.

### Negative
- Graph Runner becomes a critical dependency for the rest of the system.
- Requires disciplined release/versioning to avoid breaking downstream repos.

## Alternatives Considered
- Separate `bender-contracts` repository (rejected for now: more repos/overhead).
- Copying protos per repo (rejected: guaranteed drift).
