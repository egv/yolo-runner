# New features for yolo runner

Here are features for new version

## support for different issue trackers
- linear
- github
- startrek (yandex tracker)

## support for different agents
- codex
- claude
- kimi

## support for new vcs
- arc vcs

## planning enhancements
Right now we run ohnly one runner and implement tasks in order. We should learn how to figure out series of tasks that can be executed in parallel and do so with a given level of concurrency in separate working copies of a repository (this should work only for git now). Probably we should add some locking mechanisms, so we can gateway tasks implementation (i.e. task 3 depends on both task 1 and task 2, so we should execute 1 and 2 in paralles, wait while both of them are completed and then continue to task 3)

## linear agent
Implement linear agent so runner can be used to delegate linear tasks (including epics). More info here: https://linear.app/developers/aig
