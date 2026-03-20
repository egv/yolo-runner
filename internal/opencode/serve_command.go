package opencode

import "strings"

func BuildServeCommand(binary string) []string {
	resolvedBinary := strings.TrimSpace(binary)
	if resolvedBinary == "" {
		resolvedBinary = "opencode"
	}
	return append([]string{resolvedBinary}, BuildServeCommandArgs()...)
}

func BuildServeCommandArgs() []string {
	return []string{"serve"}
}

func buildServeBaseArgs() []string {
	return BuildServeCommandArgs()
}
