package arcreview

import (
	"strings"
	"testing"
)

func TestWithDisclosureFooter(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		author string
		want   string
	}{
		{
			name:   "appends footer once",
			body:   "Looks good, shipping as-is.",
			author: "alice",
			want:   "Looks good, shipping as-is.\n\n_Posted on behalf of @alice by yolo-agent_",
		},
		{
			name:   "trailing newline safe",
			body:   "Looks good, shipping as-is.\n\n\n",
			author: "alice",
			want:   "Looks good, shipping as-is.\n\n_Posted on behalf of @alice by yolo-agent_",
		},
		{
			name:   "trailing spaces safe",
			body:   "Reply.   \t\n",
			author: "alice",
			want:   "Reply.\n\n_Posted on behalf of @alice by yolo-agent_",
		},
		{
			name:   "empty body uses no content",
			body:   "",
			author: "alice",
			want:   "(no content)\n\n_Posted on behalf of @alice by yolo-agent_",
		},
		{
			name:   "whitespace-only body uses no content",
			body:   "  \n\t  \n",
			author: "alice",
			want:   "(no content)\n\n_Posted on behalf of @alice by yolo-agent_",
		},
		{
			name:   "author whitespace trimmed",
			body:   "Reply.",
			author: "  alice  ",
			want:   "Reply.\n\n_Posted on behalf of @alice by yolo-agent_",
		},
		{
			name:   "author leading at stripped",
			body:   "Reply.",
			author: "@bob",
			want:   "Reply.\n\n_Posted on behalf of @bob by yolo-agent_",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WithDisclosureFooter(tc.body, tc.author)
			if got != tc.want {
				t.Fatalf("WithDisclosureFooter() = %q, want %q", got, tc.want)
			}
			footer := "_Posted on behalf of @" + strings.TrimPrefix(strings.TrimSpace(tc.author), "@") + " by yolo-agent_"
			if strings.Count(got, footer) != 1 {
				t.Fatalf("expected exactly one footer %q, got:\n%s", footer, got)
			}
		})
	}
}

func TestWithDisclosureFooterIdempotent(t *testing.T) {
	body := "Continuing the thread: agreed, will refactor next."
	author := "carol"

	first := WithDisclosureFooter(body, author)
	second := WithDisclosureFooter(first, author)

	if first != second {
		t.Fatalf("expected idempotent output, first = %q, second = %q", first, second)
	}
	if c := strings.Count(second, "_Posted on behalf of @carol by yolo-agent_"); c != 1 {
		t.Fatalf("expected exactly one footer after re-application, got %d", c)
	}
}
