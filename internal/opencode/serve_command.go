package opencode

import "strings"

var buildServeCommand = BuildServeCommand

func BuildServeCommand(binary string) []string {
	resolvedBinary := strings.TrimSpace(binary)
	if resolvedBinary == "" {
		resolvedBinary = "opencode"
	}
	return append([]string{resolvedBinary}, BuildServeCommandArgs()...)
}

func BuildServeCommandArgs() []string {
	return []string{"serve", "--hostname", defaultServeHostname}
}

func buildServeBaseArgs() []string {
	return BuildServeCommandArgs()
}

func resolveServeBaseCommand(binary string) (string, []string) {
	command := buildServeCommand(binary)
	if len(command) == 0 {
		command = BuildServeCommand(binary)
	}

	resolvedBinary := strings.TrimSpace(binary)
	if len(command) > 0 && strings.TrimSpace(command[0]) != "" {
		resolvedBinary = strings.TrimSpace(command[0])
	}
	if resolvedBinary == "" {
		resolvedBinary = defaultServeBinary
	}

	args := buildServeBaseArgs()
	if len(command) > 1 {
		args = append([]string(nil), command[1:]...)
	}
	return resolvedBinary, args
}
