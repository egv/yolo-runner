package arcanum

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublishAndVerifyPRRetriesUntilArcanumReportsPublished(t *testing.T) {
	publishCalls := 0
	verifyCalls := 0
	err := PublishAndVerifyPR(context.Background(), "123", func(context.Context, string) error {
		publishCalls++
		return nil
	}, func(context.Context, string) error {
		verifyCalls++
		if verifyCalls < 2 {
			return errors.New("active diff set is draft")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("PublishAndVerifyPR() error = %v", err)
	}
	if publishCalls != 2 || verifyCalls != 2 {
		t.Fatalf("publish/verify calls = %d/%d, want 2/2", publishCalls, verifyCalls)
	}
}

func TestVerifyActiveDiffSetPublishedWithClientRejectsDraft(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "OAuth token" {
			t.Fatalf("Authorization = %q", got)
		}
		if !strings.Contains(r.URL.RawQuery, "active_diff_set") {
			t.Fatalf("query = %q, want active diff set fields", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"data":{"active_diff_set":{"id":30,"status":"draft"}}}`))
	}))
	defer server.Close()

	client, err := NewAPIClient(APIClientConfig{
		BaseURL: server.URL,
		TokenSource: func(context.Context) (string, error) {
			return "token", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}

	err = VerifyActiveDiffSetPublishedWithClient(context.Background(), client, "123")
	if err == nil || !strings.Contains(err.Error(), "status is \"draft\"") {
		t.Fatalf("VerifyActiveDiffSetPublishedWithClient() error = %v, want draft status", err)
	}
}

func TestVerifyActiveDiffSetPublishedWithClientAcceptsPublished(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"active_diff_set":{"id":30,"status":"published"}}}`))
	}))
	defer server.Close()

	client, err := NewAPIClient(APIClientConfig{
		BaseURL: server.URL,
		TokenSource: func(context.Context) (string, error) {
			return "token", nil
		},
	})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}
	if err := VerifyActiveDiffSetPublishedWithClient(context.Background(), client, "123"); err != nil {
		t.Fatalf("VerifyActiveDiffSetPublishedWithClient() error = %v", err)
	}
}
