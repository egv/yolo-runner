package distributed

import (
	"fmt"
	"strings"
)

func ValidateExecutorRegistrationPayload(payload ExecutorRegistrationPayload) error {
	if strings.TrimSpace(payload.ExecutorID) == "" {
		return fmt.Errorf("executor_id is required")
	}
	version := strings.TrimSpace(payload.CapabilitySchemaVersion)
	switch version {
	case "":
		// Backward compatible with legacy registration payloads.
		return nil
	case CapabilitySchemaVersionV1:
		if len(payload.SupportedPipelines) == 0 {
			return fmt.Errorf("supported_pipelines is required for capability schema version %s", CapabilitySchemaVersionV1)
		}
		if len(payload.SupportedAgents) == 0 {
			return fmt.Errorf("supported_agents is required for capability schema version %s", CapabilitySchemaVersionV1)
		}
		return nil
	default:
		return fmt.Errorf("unsupported capability_schema_version %q", version)
	}
}

func ValidateExecutorHeartbeatPayload(payload ExecutorHeartbeatPayload) error {
	if strings.TrimSpace(payload.ExecutorID) == "" {
		return fmt.Errorf("executor_id is required")
	}
	if payload.CurrentLoad < 0 {
		return fmt.Errorf("current_load must be >= 0")
	}
	if payload.AvailableSlots < 0 {
		return fmt.Errorf("available_slots must be >= 0")
	}
	if payload.MaxConcurrency < 0 {
		return fmt.Errorf("max_concurrency must be >= 0")
	}
	return nil
}
