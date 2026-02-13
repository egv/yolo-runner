package main

import (
	"fmt"
	"sort"
	"strings"
)

type backendCapabilities struct {
	SupportsReview bool
	SupportsStream bool
}

type backendSelectionOptions struct {
	RequireReview bool
	Stream        bool
}

func defaultBackendCapabilityMatrix() map[string]backendCapabilities {
	return map[string]backendCapabilities{
		backendOpenCode: {
			SupportsReview: true,
			SupportsStream: true,
		},
		backendCodex: {
			SupportsReview: true,
			SupportsStream: true,
		},
		backendClaude: {
			SupportsReview: true,
			SupportsStream: true,
		},
		backendKimi: {
			SupportsReview: true,
			SupportsStream: true,
		},
	}
}

func selectBackend(raw string, options backendSelectionOptions, matrix map[string]backendCapabilities) (string, backendCapabilities, error) {
	name := normalizeBackend(raw)
	caps, ok := matrix[name]
	if !ok {
		return "", backendCapabilities{}, fmt.Errorf("unsupported backend %q (supported: %s)", name, strings.Join(supportedBackends(matrix), ", "))
	}
	if options.RequireReview && !caps.SupportsReview {
		return "", backendCapabilities{}, fmt.Errorf("backend %q does not support review mode", name)
	}
	if options.Stream && !caps.SupportsStream {
		return "", backendCapabilities{}, fmt.Errorf("backend %q does not support stream mode", name)
	}
	return name, caps, nil
}

func supportedBackends(matrix map[string]backendCapabilities) []string {
	if len(matrix) == 0 {
		return nil
	}
	names := make([]string, 0, len(matrix))
	for name := range matrix {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
