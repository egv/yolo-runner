package opencode

func BuildACPArgs(repoRoot string) []string {
	return []string{"opencode", "acp", "--print-logs", "--cwd", repoRoot}
}
