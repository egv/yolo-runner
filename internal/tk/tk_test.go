package tk

import (
	"reflect"
	"strings"
	"testing"
)

func TestReadyUsesTkReadyAndQuery(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{
		"tk query": `{"id":"root","status":"open","type":"epic","priority":0}` + "\n" +
			`{"id":"root.1","status":"open","type":"task","priority":1,"parent":"root"}` + "\n" +
			`{"id":"other.1","status":"open","type":"task","priority":0,"parent":"other"}`,
		"tk ready": "root.1 [open] - Implement core contract\nother.1 [open] - Should be filtered out\n",
	}}

	a := New(r)
	issue, err := a.Ready("root")
	if err != nil {
		t.Fatalf("ready failed: %v", err)
	}

	if issue.ID != "root.1" {
		t.Fatalf("expected root.1, got %q", issue.ID)
	}
	if !r.called("tk ready") || !r.called("tk query") {
		t.Fatalf("expected tk ready and tk query to be called, got %v", r.calls)
	}
	if r.calledPrefix("tk list") {
		t.Fatalf("tk list should not be used for readiness")
	}
}

func TestUpdateStatusWithReasonUsesAddNote(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{}}
	a := New(r)

	if err := a.UpdateStatusWithReason("root.1", "blocked", "timeout happened"); err != nil {
		t.Fatalf("update status with reason failed: %v", err)
	}

	if !r.called("tk status root.1 blocked") {
		if !r.called("tk status root.1 open") {
			t.Fatalf("expected tk status/open call, got %v", r.calls)
		}
	}
	if !r.called("tk add-note root.1 blocked: timeout happened") {
		t.Fatalf("expected tk add-note call, got %v", r.calls)
	}
}

func TestShowUsesTkShowAndQuery(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{
		"tk query":       `{"id":"root.1","title":"Task title","description":"Task description","status":"open","type":"task","priority":2}`,
		"tk show root.1": "---\nid: root.1\n---\n# Task title\n",
	}}
	a := New(r)

	bead, err := a.Show("root.1")
	if err != nil {
		t.Fatalf("show failed: %v", err)
	}
	if bead.ID != "root.1" || bead.Title != "Task title" {
		t.Fatalf("unexpected bead: %#v", bead)
	}
	if !r.called("tk show root.1") || !r.called("tk query") {
		t.Fatalf("expected tk show and tk query calls, got %v", r.calls)
	}
}

func TestShowFallsBackToTitleFromTkShowOutput(t *testing.T) {
	r := &fakeRunner{responses: map[string]string{
		"tk query":       `{"id":"root.1","description":"Task description","status":"open","type":"task","priority":2}`,
		"tk show root.1": "---\nid: root.1\n---\n# Task title from show\n",
	}}
	a := New(r)

	bead, err := a.Show("root.1")
	if err != nil {
		t.Fatalf("show failed: %v", err)
	}
	if bead.ID != "root.1" || bead.Title != "Task title from show" {
		t.Fatalf("unexpected bead: %#v", bead)
	}
}

func TestParseTicketQueryParsesDependencyMetadata(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "missing dependency metadata",
			query: `{"id":"t-1","status":"open","type":"task","title":"No deps"}`,
			want:  nil,
		},
		{
			name:  "empty dependency array",
			query: `{"id":"t-1","status":"open","type":"task","title":"Empty deps","deps":[]}`,
			want:  []string{},
		},
		{
			name:  "single dependency",
			query: `{"id":"t-1","status":"open","type":"task","title":"Single dep","deps":["d-1"]}`,
			want:  []string{"d-1"},
		},
		{
			name:  "multiple dependencies",
			query: `{"id":"t-1","status":"open","type":"task","title":"Multiple deps","deps":["d-1","d-2"]}`,
			want:  []string{"d-1", "d-2"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tickets, err := parseTicketQuery(tc.query)
			if err != nil {
				t.Fatalf("parseTicketQuery failed: %v", err)
			}
			if len(tickets) != 1 {
				t.Fatalf("expected one parsed ticket, got %d", len(tickets))
			}
			got := []string(tickets[0].Deps)
			if len(tc.want) == 0 {
				if len(got) != len(tc.want) {
					t.Fatalf("expected %d dependencies, got %d (%v)", len(tc.want), len(got), got)
				}
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("expected dependencies %v, got %v", tc.want, got)
			}
		})
	}
}

func TestParseTicketQueryAcceptsMalformedDependencyMetadataAsSingleDependencyString(t *testing.T) {
	output := `{"id":"t-1","status":"open","type":"task","title":"Legacy dep format","deps":"d-1"}`

	tickets, err := parseTicketQuery(output)
	if err != nil {
		t.Fatalf("parseTicketQuery failed: %v", err)
	}
	if len(tickets) != 1 {
		t.Fatalf("expected one parsed ticket, got %d", len(tickets))
	}
	got := []string(tickets[0].Deps)
	if !reflect.DeepEqual(got, []string{"d-1"}) {
		t.Fatalf("expected malformed dependency metadata to normalize to single dependency, got %v", got)
	}
}

type fakeRunner struct {
	responses map[string]string
	calls     []string
}

func (f *fakeRunner) Run(args ...string) (string, error) {
	joined := strings.Join(args, " ")
	f.calls = append(f.calls, joined)
	if out, ok := f.responses[joined]; ok {
		return out, nil
	}
	if out, ok := f.responses[args[0]+" "+args[1]]; ok {
		return out, nil
	}
	return "", nil
}

func (f *fakeRunner) called(cmd string) bool {
	for _, call := range f.calls {
		if call == cmd {
			return true
		}
	}
	return false
}

func (f *fakeRunner) calledPrefix(prefix string) bool {
	for _, call := range f.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}
