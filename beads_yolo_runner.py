from __future__ import annotations

import argparse
import json
import os
import subprocess
import time
from pathlib import Path


def build_opencode_agent_path(repo_root: str) -> str:
    return f"{repo_root}/.opencode/agent/yolo.md"


def select_first_open_leaf_task(tree: list[dict]) -> str | None:
    def sorted_items(items: list[dict]) -> list[dict]:
        return sorted(items, key=lambda item: item.get("priority", 999))

    def find_leaf(items):
        for item in sorted_items(items):
            if item.get("status") != "open":
                continue
            issue_type = item.get("issue_type")
            if issue_type == "epic":
                children = item.get("children", [])
                if not children:
                    continue
                leaf = find_leaf(children)
                if leaf:
                    return leaf
                continue
            if issue_type == "task":
                return item.get("id")
        return None

    return find_leaf(tree)


def build_opencode_command(repo_root: str, prompt: str) -> list[str]:
    return [
        "opencode",
        "run",
        prompt,
        "--agent",
        "yolo",
        "--format",
        "json",
        repo_root,
    ]


def build_opencode_env(
    base_env: dict[str, str] | None = None,
    config_root: Path | None = None,
    config_dir: Path | None = None,
) -> dict[str, str]:
    env = dict(base_env or os.environ)
    env["OPENCODE_DISABLE_CLAUDE_CODE"] = "true"
    env["OPENCODE_DISABLE_CLAUDE_CODE_SKILLS"] = "true"
    env["OPENCODE_DISABLE_CLAUDE_CODE_PROMPT"] = "true"
    env["OPENCODE_DISABLE_DEFAULT_PLUGINS"] = "true"
    env["CI"] = "true"

    if config_root is not None:
        config_root.mkdir(parents=True, exist_ok=True)
        env["XDG_CONFIG_HOME"] = str(config_root)

    if config_dir is not None:
        config_dir.mkdir(parents=True, exist_ok=True)
        config_file = config_dir / "opencode.json"
        if not config_file.exists():
            config_file.write_text("{}", encoding="utf-8")
        env["OPENCODE_CONFIG_DIR"] = str(config_dir)
        env["OPENCODE_CONFIG"] = str(config_file)
        env["OPENCODE_CONFIG_CONTENT"] = "{}"

    return env


def build_opencode_prompt(issue_id: str, title: str, description: str, acceptance: str) -> str:
    return f"""You are in YOLO mode - all permissions granted.

Your task is: {issue_id} - {title}

**Description:**
{description}

**Acceptance Criteria:**
{acceptance}

**Strict TDD Protocol:**
1. Write failing tests based on acceptance criteria
2. Run tests to confirm they fail
3. Write minimal implementation to pass each test
4. Run tests and ensure all pass
5. Do not modify unrelated files
6. If tests fail, fix and rerun

**Rules:**
- NEVER write implementation code before a failing test exists
- Watch test fail before writing code
- Write minimal code to pass each test
- Do not modify unrelated files
- Use real code, not mocks unless unavoidable
- All tests must pass before marking task complete

Start now by analyzing the codebase and writing your first failing test.
"""


def log_completion(
    log_path: Path,
    issue_id: str,
    title: str,
    commit_sha: str,
    status: str = "completed",
) -> None:
    log_path.parent.mkdir(parents=True, exist_ok=True)
    entry = {
        "timestamp": subprocess.check_output(["date", "+%Y-%m-%dT%H:%M:%SZ"], text=True).strip(),
        "issue_id": issue_id,
        "title": title,
        "status": status,
        "commit_sha": commit_sha,
    }
    with log_path.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(entry) + "\n")


def select_first_open_leaf_task_id(root_id: str, command_runner) -> str | None:
    def load_children(parent_id: str) -> list[dict]:
        output = command_runner(
            ["bd", "ready", "--parent", parent_id, "--json"],
            text=True,
        )
        return json.loads(output)

    def sorted_items(items: list[dict]) -> list[dict]:
        return sorted(items, key=lambda item: item.get("priority", 999))

    def walk(parent_id: str) -> str | None:
        for item in sorted_items(load_children(parent_id)):
            if item.get("status") != "open":
                continue
            issue_type = item.get("issue_type")
            issue_id = item.get("id")
            if not issue_id:
                continue
            if issue_type == "task":
                return issue_id
            if issue_type == "epic":
                leaf = walk(issue_id)
                if leaf:
                    return leaf
        return None

    return walk(root_id)


def run_once(
    repo_root: str,
    command_runner,
    call_runner=subprocess.check_call,
    opencode_runner=None,
    log_writer=log_completion,
    log_path: Path | None = None,
    root_id: str = "algi-8bt",
    dry_run: bool = False,
) -> str:
    leaf_id = select_first_open_leaf_task_id(root_id, command_runner)
    if not leaf_id:
        return "no_tasks"

    task_data = json.loads(command_runner(["bd", "show", leaf_id, "--json"], text=True))[0]
    title = task_data.get("title", "")
    description = task_data.get("description", "")
    acceptance = task_data.get("acceptance_criteria", "")

    prompt = build_opencode_prompt(leaf_id, title, description, acceptance)
    command = build_opencode_command(repo_root, prompt)

    if dry_run:
        print(f"Task: {leaf_id} - {title}")
        print(prompt)
        print("Command:", " ".join(command))
        return "dry_run"

    if opencode_runner is None:
        opencode_runner = subprocess.check_call

    call_runner(["bd", "update", leaf_id, "--status", "in_progress"])
    config_root = Path.home() / ".config" / "opencode-runner"
    config_dir = config_root / "opencode"
    log_root = Path(repo_root) / "runner-logs" / "opencode"
    log_root.mkdir(parents=True, exist_ok=True)
    opencode_log = log_root / f"{leaf_id}.jsonl"
    print(f"Starting {leaf_id}: {title}")
    start_time = time.time()
    with opencode_log.open("w", encoding="utf-8") as handle:
        opencode_runner(
            command,
            env=build_opencode_env(config_root=config_root, config_dir=config_dir),
            stdout=handle,
        )
    elapsed = time.time() - start_time
    print(f"OpenCode finished in {elapsed:.1f}s")

    call_runner(["git", "add", "."])
    status_output = command_runner(["git", "status", "--porcelain"], text=True)
    if not status_output.strip():
        commit_sha = command_runner(["git", "rev-parse", "HEAD"], text=True).strip()
        target_log_path = log_path or Path(repo_root) / "runner-logs" / "beads_yolo_runner.jsonl"
        log_writer(target_log_path, leaf_id, title, commit_sha, status="blocked")
        call_runner(["bd", "update", leaf_id, "--status", "blocked"])
        return "blocked"

    commit_message = f"feat: {title.lower()}" if title else "feat: complete bead task"
    call_runner(["git", "commit", "-m", commit_message])

    commit_sha = command_runner(["git", "rev-parse", "HEAD"], text=True).strip()
    target_log_path = log_path or Path(repo_root) / "runner-logs" / "beads_yolo_runner.jsonl"
    log_writer(target_log_path, leaf_id, title, commit_sha, status="completed")

    call_runner(["bd", "close", leaf_id])
    closed_status = json.loads(command_runner(["bd", "show", leaf_id, "--json"], text=True))[0].get("status")
    if closed_status != "closed":
        log_writer(target_log_path, leaf_id, title, commit_sha, status="blocked")
        call_runner(["bd", "update", leaf_id, "--status", "blocked"])
        return "blocked"
    call_runner(["bd", "sync"])
    return "completed"


def run_loop(repo_root: str, run_once_runner=run_once, max_tasks: int | None = None, root_id: str = "algi-8bt", dry_run: bool = False) -> int:
    completed = 0
    while True:
        if max_tasks is not None and completed >= max_tasks:
            break
        result = run_once_runner(
            repo_root=repo_root,
            command_runner=subprocess.check_output,
            root_id=root_id,
            dry_run=dry_run,
        )
        if result == "no_tasks":
            break
        if result == "completed":
            completed += 1
        if result == "dry_run":
            break
    return completed


def main() -> None:
    parser = argparse.ArgumentParser(description="Beads YOLO runner")
    parser.add_argument("--repo", default=".", help="Repository root path")
    parser.add_argument("--root", default="algi-8bt", help="Root bead/epic ID")
    parser.add_argument("--max", type=int, default=None, help="Max tasks to process")
    parser.add_argument("--dry-run", action="store_true", help="Print task and prompt without executing")
    args = parser.parse_args()
    completed = run_loop(
        repo_root=args.repo,
        max_tasks=args.max,
        root_id=args.root,
        dry_run=args.dry_run,
    )
    print(f"Completed {completed} tasks")


if __name__ == "__main__":
    main()
