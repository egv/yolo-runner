package opencode

const serveLoopbackHostname = "127.0.0.1"

func BuildACPArgs(repoRoot string) []string {
	return BuildACPArgsWithModel(repoRoot, "")
}

func BuildACPArgsWithModel(repoRoot string, model string) []string {
	args := []string{"opencode", "acp", "--print-logs", "--log-level", "DEBUG", "--cwd", repoRoot}
	return args
}

func BuildServeArgs() []string {
	return []string{"opencode", "serve", "--print-logs", "--log-level", "DEBUG", "--hostname", serveLoopbackHostname}
}
