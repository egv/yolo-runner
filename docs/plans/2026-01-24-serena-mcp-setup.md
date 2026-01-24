# Serena MCP Setup Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add project-local Serena MCP configuration for OpenCode and provide a concise install guide for other repos.

**Architecture:** Use OpenCode’s project config discovery by adding `.opencode/opencode.jsonc` with a local MCP server entry. Provide a standalone `INSTALL_SERENA.md` with copyable steps to install Serena in any repo (project-local only).

**Tech Stack:** OpenCode config JSONC, Serena (uvx), markdown docs.

### Task 1: Create project-local MCP config

**Files:**
- Create: `.opencode/opencode.jsonc`

**Step 1: Write the failing test**

Not applicable (config file).

**Step 2: Implement minimal config**

Create `.opencode/opencode.jsonc` with:
```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "serena": {
      "type": "local",
      "command": [
        "uvx",
        "--from",
        "git+https://github.com/oraios/serena",
        "serena",
        "start-mcp-server",
        "--context",
        "ide",
        "--project",
        "."
      ]
    }
  }
}
```

**Step 3: Verify config discovery (manual)**

Run: `opencode mcp status` (or start opencode and check MCP list).
Expected: `serena` appears as configured (connected if uvx available).

**Step 4: Commit**

```bash
git add .opencode/opencode.jsonc
git commit -m "chore: add Serena MCP config"
```

### Task 2: Add concise install guide

**Files:**
- Create: `INSTALL_SERENA.md`

**Step 1: Write the failing test**

Not applicable (doc).

**Step 2: Write concise instructions**

Include:
- Purpose: project-local Serena MCP for OpenCode
- Requirements: `uvx` available
- Steps:
  1) Create `.opencode/opencode.jsonc` with the MCP block above
  2) Run `opencode mcp status` to verify
- Note: Use `--project .` and `--context ide`
- Copy-paste ready snippet

**Step 3: Commit**

```bash
git add INSTALL_SERENA.md
git commit -m "docs: add Serena install guide"
```

### Task 3: Verify and finalize

**Files:**
- Test: none

**Step 1: Optional sanity check**

Run: `opencode mcp status`
Expected: `serena` present.

**Step 2: Commit (if not already)**

If both tasks are in a single commit, combine the adds and commit once.

**Step 3: Push**

Run: `git pull --rebase`, `bd sync`, `git push`, `git status`.
