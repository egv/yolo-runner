package opencode

func BuildACPArgs(repoRoot string) []string {
	return BuildACPArgsWithModel(repoRoot, "")
}

func BuildACPArgsWithModel(repoRoot string, model string) []string {
	args := []string{"opencode", "acp", "--print-logs", "--cwd", repoRoot}
	if model != "" {
		args = append(args, "--model", model)
	}
	return args
}
