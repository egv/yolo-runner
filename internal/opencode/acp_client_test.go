package opencode

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	acp "github.com/ironpark/acp-go"
)

func TestACPHandlerAutoApprovesPermission(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "log.jsonl")
	handler := NewACPHandler("issue-1", logPath, nil)
	decision := handler.HandlePermission(context.Background(), "perm-1", "repo.write")
	if decision != ACPDecisionAllow {
		t.Fatalf("expected allow, got %v", decision)
	}
}

func TestACPClientCancelsQuestionPermission(t *testing.T) {
	var gotKind string
	var gotOutcome string
	handler := NewACPHandler("issue-1", "log", func(_ string, _ string, kind string, outcome string) error {
		gotKind = kind
		gotOutcome = outcome
		return nil
	})
	client := &acpClient{handler: handler}
	questionKind := acp.ToolKind("question")

	response, err := client.RequestPermission(context.Background(), &acp.RequestPermissionRequest{
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: acp.ToolCallId("tool-1"),
			Title:      "Need input",
			Kind:       &questionKind,
		},
		Options: []acp.PermissionOption{
			{
				Kind:     acp.PermissionOptionKindAllowOnce,
				Name:     "Allow",
				OptionId: acp.PermissionOptionId("allow"),
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotKind != "question" {
		t.Fatalf("expected question handler, got %q", gotKind)
	}
	if gotOutcome != "decide yourself" {
		t.Fatalf("expected question outcome, got %q", gotOutcome)
	}

	expected := acp.NewRequestPermissionOutcomeCancelled()
	if !reflect.DeepEqual(response.Outcome, expected) {
		t.Fatalf("expected cancelled outcome, got %#v", response.Outcome)
	}
}
