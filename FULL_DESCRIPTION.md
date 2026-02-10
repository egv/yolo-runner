# This is how yolo runner should truly work

## Parts of the system

This system consists of several lousely coupled parts. Here are their names and descriptions

### Tasks manager
This part abstracts out interface to the task tracking systems. It should be able to
- provide list of next tasks for givent parent task. This list is ordered and sorted based on priority and task dependencies.
- give task info by it's id
- set task status (open/closed/in-progress/blocked) by its id
- set task data by it's id (for updating tasks with block reasons etc.)

This part should abstract out different task tracking services, namely beads, linear, github issues etc. First version should use tk for this, but the foundation should be solid to easily integrate all other trackers.

### Runner 
Given the task details and context like task parent (this is still in the works, so lets be very generic here), it runs coding agent in yolo mode (hence the name), and then monitors it for completion. This should also be quite modular, allowing different CLI agents to be used here, for ex. opencode, claude, codex, kimi etc.

Now main agent is opencode, which is being run as an ACP server. Probably we should try the new yolo flag in it, but now we have our own agent spec and run it in ACP mode.

Runner should provide quite verbose on what exactly is going on for those who is interested.

### Agent itself

It should get tasks list from the tasks part, prepare context and task description to the runner part, execute runner, verify it's results or process it's errors, update current task accordingly, pick the next task, and loop until there are no more tasks or until stopped. Failed tasks should be retried for a given number of times and then marked as so.

Of cource this part should also provide verbose logging for those, who are interested. Even more, it should forward runner's logs as well

### TUI 

This should collect verbose logs from the agent (and, therefore, the runner), process them, and provide a clean and informative TUI for the whole system. TUI is more for process monitoring than for any control or changes, at least at this point. 

## Implementation detail

- golang
- bubbletea, bubbles and related libs for TUI
- unix-way composability since I plan to reuse those things
- all parts should be CLI utilities that play well together
