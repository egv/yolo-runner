package arcreview

import (
	"bytes"
	"encoding/json"
	"fmt"
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

	// Models do not always emit a bare JSON object: they may wrap it in a
	// Markdown code fence or surround it with prose (a Cyrillic preamble, a
	// trailing sign-off). Extract the first balanced JSON object that decodes
	// into a review-shaped result rather than requiring the whole payload to
	// be exactly that object.
	result, err := extractReviewResultJSON(payload)
	if err != nil {
		return ReviewResult{}, fmt.Errorf("parse review result: %w", err)
	}
	// Tolerate a missing summary: the model sometimes omits it even when the
	// rest of the review (inline comments, verdict) is well-formed. Use a
	// neutral fallback rather than rejecting the whole review.
	if strings.TrimSpace(result.Summary) == "" {
		result.Summary = "Review completed."
	}
	return result, nil
}

func extractReviewResultJSON(payload []byte) (ReviewResult, error) {
	candidate := stripMarkdownFences(bytes.TrimSpace(payload))

	// Prefer a review object that has a summary; fall back to the first valid
	// review-shaped JSON object (one with any of summary, inline_comments,
	// replies, or ship) so a missing summary alone doesn't reject the review.
	var fallback *ReviewResult
	var lastErr error
	for i := 0; i < len(candidate); i++ {
		if candidate[i] != '{' {
			continue
		}
		object, ok := balancedJSONObject(candidate[i:])
		if !ok {
			continue
		}
		var result ReviewResult
		if err := json.Unmarshal(object, &result); err != nil {
			lastErr = err
			continue
		}
		if !looksLikeReviewResult(object) {
			continue
		}
		if strings.TrimSpace(result.Summary) != "" {
			return result, nil
		}
		if fallback == nil {
			fallback = &result
		}
	}
	if fallback != nil {
		return *fallback, nil
	}
	if lastErr != nil {
		return ReviewResult{}, lastErr
	}
	return ReviewResult{}, fmt.Errorf("no JSON object found in review output")
}

// looksLikeReviewResult reports whether a decoded JSON object has any field
// that identifies it as a review (rather than a stray brace pair in prose).
func looksLikeReviewResult(object []byte) bool {
	return bytes.Contains(object, []byte("summary")) ||
		bytes.Contains(object, []byte("inline_comments")) ||
		bytes.Contains(object, []byte("replies")) ||
		bytes.Contains(object, []byte("ship")) ||
		bytes.Contains(object, []byte("blockers"))
}

// stripMarkdownFences removes a single surrounding ```/```json code fence when
// the payload is fully wrapped in one, leaving the inner content untouched
// otherwise.
func stripMarkdownFences(payload []byte) []byte {
	trimmed := bytes.TrimSpace(payload)
	if !bytes.HasPrefix(trimmed, []byte("```")) {
		return trimmed
	}
	// Drop the opening fence line (``` or ```json) and the closing fence.
	if idx := bytes.IndexByte(trimmed, '\n'); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	if idx := bytes.LastIndex(trimmed, []byte("```")); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	return bytes.TrimSpace(trimmed)
}

// balancedJSONObject returns the substring from the leading '{' through its
// matching '}', honoring string literals and escapes so that braces inside
// strings do not unbalance the scan. ok is false if no matching brace exists.
func balancedJSONObject(data []byte) ([]byte, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return data[:i+1], true
			}
		}
	}
	return nil, false
}
