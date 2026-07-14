package arcanum

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestListReviewPRsForRolesLive hits the real Arcanum API for the login in
// YOLO_LIVE_PR_LIST_LOGIN and asserts every returned PR has that login as the
// author or an assigned reviewer — never subscriber-only. Guarded so it never
// runs in CI.
func TestListReviewPRsForRolesLive(t *testing.T) {
	login := os.Getenv("YOLO_LIVE_PR_LIST_LOGIN")
	if login == "" {
		t.Skip("set YOLO_LIVE_PR_LIST_LOGIN to run the live discovery check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	client, err := NewAPIClient(APIClientConfig{BaseURL: DefaultAPIBaseURL, TokenSource: DefaultAPITokenSource})
	if err != nil {
		t.Fatalf("NewAPIClient() error = %v", err)
	}
	prs, err := ListReviewPRsForRoles(ctx, client, login, login)
	if err != nil {
		t.Fatalf("ListReviewPRsForRoles() error = %v", err)
	}
	for _, pr := range prs {
		role := ""
		if pr.Author == login {
			role = "author"
		}
		for _, reviewer := range pr.Reviewers {
			if reviewer == login {
				role += "+reviewer"
			}
		}
		t.Logf("PR %s author=%s role=%q reviewers=%v summary=%q", pr.ID, pr.Author, role, pr.Reviewers, pr.Summary)
		if role == "" {
			t.Errorf("PR %s: %s is neither author nor assigned reviewer (subscriber leak)", pr.ID, login)
		}
	}
}
