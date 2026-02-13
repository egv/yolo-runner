package webhook

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/anomalyco/yolo-runner/internal/linear"
)

func buildSessionStep(event linear.AgentSessionEvent, payload []byte) string {
	sessionID := sessionStepSessionID(event, payload)

	switch event.Action {
	case linear.AgentSessionEventActionCreated:
		return fmt.Sprintf("%s:created", sessionID)
	case linear.AgentSessionEventActionPrompted:
		if event.AgentActivity != nil {
			if activityID := strings.TrimSpace(event.AgentActivity.ID); activityID != "" {
				return fmt.Sprintf("%s:prompted:%s", sessionID, activityID)
			}
		}
		if eventID := strings.TrimSpace(event.ID); eventID != "" {
			return fmt.Sprintf("%s:prompted:event:%s", sessionID, eventID)
		}
		return fmt.Sprintf("%s:prompted:fingerprint:%s", sessionID, payloadFingerprint(payload))
	default:
		action := strings.TrimSpace(string(event.Action))
		if action == "" {
			action = "unknown"
		}
		if eventID := strings.TrimSpace(event.ID); eventID != "" {
			return fmt.Sprintf("%s:%s:event:%s", sessionID, action, eventID)
		}
		return fmt.Sprintf("%s:%s:fingerprint:%s", sessionID, action, payloadFingerprint(payload))
	}
}

func buildIdempotencyKey(sessionStep string) string {
	return fmt.Sprintf("%s:%s", jobIdempotencyNamespace1, strings.TrimSpace(sessionStep))
}

func sessionStepSessionID(event linear.AgentSessionEvent, payload []byte) string {
	if sessionID := strings.TrimSpace(event.AgentSession.ID); sessionID != "" {
		return sessionID
	}
	return "session-" + payloadFingerprint(payload)
}

func payloadFingerprint(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:8])
}
