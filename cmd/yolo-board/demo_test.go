package main

import (
	"bytes"
	"io"
	"testing"
)

func TestRunMainDemoStateGoldenRender(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	code := RunMain([]string{"--demo-state"}, nil, &out, &errOut)
	if code != 0 {
		t.Fatalf("RunMain() exit code = %d, want 0; stderr=%q", code, errOut.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}

	const want = `polling 3 items across 2 sources

Collectors
NAME	TYPE	PENDING	ACTIVE	DONE	LAST POLL	LAST ERROR	HEARTBEAT
> github	github	1	0	1	25m0s	-	20m0s
  startrek	startrek	0	1	0	2m0s	-	-

---
polling 3 items across 2 sources

Queue
Counts: done=1 open=1 running=1
KIND	SOURCE_REF	PRESET	PRIORITY	STATE	ATTEMPT	CLAIMED_BY	AGE
> pr-review	#43	author	1	done	1	-	3h0m0s
  implement	#42	codex	3	open	0	-	30m0s
  review	FLEET-7	gpt-5	2	running	1	runner-alpha	2h0m0s

---
polling 3 items across 2 sources

Runners
ID	PID	PRESETS	CAP	HEARTBEAT	CURRENT
> runner-alpha	4242	codex,gpt-5	1	1m0s	review startrek/FLEET-7
  runner-beta	4343	codex	2	15m0s	-

---
polling 3 items across 2 sources

Item yolo-18-b
Fields
ID	KIND	SOURCE	SOURCE_REF	PRESET	PRIORITY	STATE	ATTEMPT	CLAIMED_BY
yolo-18-b	review	startrek	FLEET-7	gpt-5	2	running	1	runner-alpha
IDEMPOTENCY_KEY	MAX_ATTEMPTS	NOT_BEFORE	LEASE_EXPIRES_AT	HEARTBEAT_AT	CREATED_AT	UPDATED_AT
startrek:FLEET-7	3	-	2026-06-30T12:04:00Z	2026-06-30T11:59:00Z	2026-06-30T10:00:00Z	2026-06-30T11:58:00Z
Payload
{"title":"Review fleet TUI"}

Blocks
ID	KIND	SOURCE_REF	STATE
yolo-18-a	implement	#42	open

BlockedBy
-

Result
-

Live events
agent_progress	runner-alpha	applying patch
`
	if got := out.String(); got != want {
		t.Fatalf("demo render mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestRunMainDemoStateIgnoresInput(t *testing.T) {
	var out bytes.Buffer

	code := RunMain([]string{"--demo-state"}, bytes.NewBufferString("not event json"), &out, io.Discard)
	if code != 0 {
		t.Fatalf("RunMain() exit code = %d, want 0", code)
	}
}
