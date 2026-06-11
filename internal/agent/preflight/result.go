package preflight

import (
	"encoding/json"
	"strings"
)

type Decision string

const (
	DecisionReady     Decision = "ready"
	DecisionNeedsInfo Decision = "needs_info"
	DecisionReply     Decision = "reply"

	MinimumReadyConfidence = 0.8
)

type Result struct {
	Decision   Decision `json:"decision"`
	Confidence float64  `json:"confidence"`
	Summary    string   `json:"summary"`
	Questions  []string `json:"questions"`
	ReplyText  string   `json:"reply_text"`
}

func ParseResult(output string) Result {
	var result Result
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &result); err != nil {
		return Result{Decision: DecisionNeedsInfo}
	}

	result.Decision = normalizeDecision(result.Decision)
	result.Summary = strings.TrimSpace(result.Summary)
	result.Questions = trimQuestions(result.Questions)
	result.ReplyText = strings.TrimSpace(result.ReplyText)
	if result.Decision == DecisionReady && result.Confidence < MinimumReadyConfidence {
		result.Decision = DecisionNeedsInfo
	}
	if result.Decision == DecisionReply && result.ReplyText == "" {
		result.Decision = DecisionNeedsInfo
	}
	return result
}

func normalizeDecision(decision Decision) Decision {
	switch Decision(strings.ToLower(strings.TrimSpace(string(decision)))) {
	case DecisionReady:
		return DecisionReady
	case DecisionNeedsInfo:
		return DecisionNeedsInfo
	case DecisionReply:
		return DecisionReply
	default:
		return DecisionNeedsInfo
	}
}

func trimQuestions(questions []string) []string {
	if questions == nil {
		return nil
	}
	trimmed := make([]string, 0, len(questions))
	for _, question := range questions {
		if question = strings.TrimSpace(question); question != "" {
			trimmed = append(trimmed, question)
		}
	}
	return trimmed
}
