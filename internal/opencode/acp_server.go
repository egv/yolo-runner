package opencode

import "strconv"

func BuildACPArgs(repoRoot string, port int) []string {
	return []string{"opencode", "acp", "--port", strconv.Itoa(port), "--cwd", repoRoot}
}
