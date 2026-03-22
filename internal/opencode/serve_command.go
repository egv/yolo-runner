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

	injectedBinary := resolveServeBinary(binary)
	binaryIndex := indexOfTrimmedArg(command, injectedBinary)
	serveIndex := indexOfTrimmedArg(command, "serve")

	switch {
	case binaryIndex == 0:
		return injectedBinary, ensureServeBaseArgs(command[1:])
	case binaryIndex > 0:
		return firstNonEmptyTrimmed(command[0], injectedBinary), ensureServeBaseArgs(command[1:])
	case serveIndex == 0:
		return injectedBinary, append([]string(nil), command...)
	case serveIndex > 0:
		resolvedBinary := firstNonEmptyTrimmed(command[0], injectedBinary)
		args := append([]string(nil), command[1:serveIndex]...)
		if resolvedBinary != injectedBinary {
			args = append(args, injectedBinary)
		}
		args = append(args, command[serveIndex:]...)
		return resolvedBinary, args
	default:
		resolvedBinary := injectedBinary
		if len(command) > 0 {
			resolvedBinary = firstNonEmptyTrimmed(command[0], injectedBinary)
		}

		args := make([]string, 0, len(command)+len(buildServeBaseArgs()))
		if len(command) > 1 {
			args = append(args, command[1:]...)
		}
		if resolvedBinary != injectedBinary {
			args = append(args, injectedBinary)
		}
		args = append(args, buildServeBaseArgs()...)
		return resolvedBinary, args
	}
}

func resolveServeBinary(binary string) string {
	resolvedBinary := strings.TrimSpace(binary)
	if resolvedBinary == "" {
		return defaultServeBinary
	}
	return resolvedBinary
}

func ensureServeBaseArgs(args []string) []string {
	cloned := append([]string(nil), args...)
	if indexOfTrimmedArg(cloned, "serve") >= 0 {
		return cloned
	}
	return append(cloned, buildServeBaseArgs()...)
}

func firstNonEmptyTrimmed(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" {
		return trimmed
	}
	return fallback
}

func indexOfTrimmedArg(args []string, target string) int {
	target = strings.TrimSpace(target)
	if target == "" {
		return -1
	}
	for i, arg := range args {
		if strings.TrimSpace(arg) == target {
			return i
		}
	}
	return -1
}
