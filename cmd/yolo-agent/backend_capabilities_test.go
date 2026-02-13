package main

import (
	"strings"
	"testing"
)

func TestSelectBackendRejectsUnknownBackend(t *testing.T) {
	_, _, err := selectBackend("unknown", backendSelectionOptions{}, defaultBackendCapabilityMatrix())
	if err == nil {
		t.Fatalf("expected unknown backend to fail")
	}
	if !strings.Contains(err.Error(), `unsupported backend "unknown"`) {
		t.Fatalf("expected unsupported backend error, got %q", err.Error())
	}
}

func TestSelectBackendRejectsMissingReviewCapability(t *testing.T) {
	matrix := map[string]backendCapabilities{
		backendOpenCode: {
			SupportsReview: false,
			SupportsStream: true,
		},
	}

	_, _, err := selectBackend(backendOpenCode, backendSelectionOptions{RequireReview: true}, matrix)
	if err == nil {
		t.Fatalf("expected missing review capability to fail")
	}
	if !strings.Contains(err.Error(), `does not support review mode`) {
		t.Fatalf("expected review capability error, got %q", err.Error())
	}
}

func TestSelectBackendRejectsMissingStreamCapability(t *testing.T) {
	matrix := map[string]backendCapabilities{
		backendOpenCode: {
			SupportsReview: true,
			SupportsStream: false,
		},
	}

	_, _, err := selectBackend(backendOpenCode, backendSelectionOptions{Stream: true}, matrix)
	if err == nil {
		t.Fatalf("expected missing stream capability to fail")
	}
	if !strings.Contains(err.Error(), `does not support stream mode`) {
		t.Fatalf("expected stream capability error, got %q", err.Error())
	}
}

func TestSelectBackendReturnsCapabilitiesWhenSupported(t *testing.T) {
	name, caps, err := selectBackend(" codex ", backendSelectionOptions{RequireReview: true, Stream: true}, defaultBackendCapabilityMatrix())
	if err != nil {
		t.Fatalf("expected backend selection to succeed, got %v", err)
	}
	if name != backendCodex {
		t.Fatalf("expected backend %q, got %q", backendCodex, name)
	}
	if !caps.SupportsReview {
		t.Fatalf("expected codex SupportsReview=true")
	}
	if !caps.SupportsStream {
		t.Fatalf("expected codex SupportsStream=true")
	}
}
