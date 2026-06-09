package arcreview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type ReviewResult struct {
	Summary        string                `json:"summary"`
	InlineComments []ReviewInlineComment `json:"inline_comments"`
	Replies        []ReviewReply         `json:"replies"`
	Blockers       []ReviewBlocker       `json:"blockers"`
	Ship           ReviewShipDecision    `json:"ship"`
}

type ReviewInlineComment struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Body     string `json:"body"`
	Severity string `json:"severity"`
}

type ReviewReply struct {
	CommentID string `json:"comment_id"`
	Body      string `json:"body"`
}

type ReviewBlocker struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

type ReviewShipDecision struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

func ParseReviewResult(payload []byte) (ReviewResult, error) {
	if strings.TrimSpace(string(payload)) == "" {
		return ReviewResult{}, fmt.Errorf("review result payload is required")
	}

	var result ReviewResult
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&result); err != nil {
		return ReviewResult{}, fmt.Errorf("parse review result: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ReviewResult{}, fmt.Errorf("parse review result: trailing JSON content")
	}
	if strings.TrimSpace(result.Summary) == "" {
		return ReviewResult{}, fmt.Errorf("review result summary is required")
	}
	return result, nil
}
