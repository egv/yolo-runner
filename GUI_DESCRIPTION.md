# this is how GUI should work

- it should collect data from stdin into internal state
- state should be used for rendering later
- state does not contain verbation elements from jsonl, but rather is some derivative from them. State is some kind of structure, with fields getting modified by events from the stdin
- for example is should contain some basic data like initial run params etc., and then array of states for each worker, which, in turn, contain data about each runner state, which is, it turn, some other data with array of events by id, each of them has type (rougly equal to the acp message type) and data and state like started, in progress etc
- this way we can have elm like architecture for bubble tea lib and bubbles and lipgloss
- I see UI like a statusbar with main data (like activity indicator, total run time, number of completed/inprogress/total tasks etc.), and a scrollable list of expandable data for each worker, then each of it's tasks etc.
- each element has only base data in the collapsed state and all data in the expanded state
