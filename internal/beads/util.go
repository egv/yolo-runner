package beads

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Runner interface {
	Run(args ...string) (string, error)
}

func traceJSONParse(operation string, data []byte, target interface{}) error {
	err := json.Unmarshal(data, target)
	if err == nil {
		return nil
	}

	if payload := extractJSONPayload(data); len(payload) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(payload))
		if err := decoder.Decode(target); err == nil {
			return nil
		}
	}

	fmt.Fprintf(os.Stderr, "JSON parse error in %s: %v\n", operation, err)
	fmt.Fprintf(os.Stderr, "First 200 bytes: %q\n", string(data[:min(200, len(data))]))
	return err
}

func extractJSONPayload(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '[' || trimmed[0] == '{' {
		return trimmed
	}
	for _, marker := range [][]byte{[]byte("\n["), []byte("\n{"), []byte("\r\n["), []byte("\r\n{")} {
		if idx := bytes.Index(data, marker); idx >= 0 {
			return bytes.TrimSpace(data[idx+len(marker)-1:])
		}
	}
	if idx := bytes.IndexAny(data, "[{"); idx >= 0 {
		return bytes.TrimSpace(data[idx:])
	}
	return nil
}

func sanitizeReason(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.ReplaceAll(trimmed, "\r\n", "\n")
	trimmed = strings.ReplaceAll(trimmed, "\r", "\n")
	trimmed = strings.ReplaceAll(trimmed, "\n", "; ")
	const maxLen = 500
	if len(trimmed) > maxLen {
		return truncateRunes(trimmed, maxLen)
	}
	return trimmed
}

func truncateRunes(input string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	count := 0
	for i := range input {
		if count == maxRunes {
			return input[:i]
		}
		count++
	}
	return input
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
