package arcreview

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const MaxProjectConventionsBytes = 16 * 1024

var projectConventionFiles = []string{"CLAUDE.md", "AGENTS.md"}

func ReadProjectConventions(workspace string, projectRoot string) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "", fmt.Errorf("workspace is required")
	}

	rootPath, err := projectConventionsRootPath(workspace, projectRoot)
	if err != nil {
		return "", err
	}

	sections := make([]string, 0, len(projectConventionFiles))
	for _, name := range projectConventionFiles {
		contents, ok, err := readProjectConventionFile(filepath.Join(rootPath, name))
		if err != nil {
			return "", err
		}
		if ok {
			sections = append(sections, name+":\n"+contents)
		}
	}

	return limitProjectConventions(strings.Join(sections, "\n\n")), nil
}

func projectConventionsRootPath(workspace string, projectRoot string) (string, error) {
	projectRoot = strings.TrimSpace(filepath.ToSlash(projectRoot))
	if projectRoot == "" || projectRoot == "." {
		return workspace, nil
	}
	projectRoot = strings.TrimPrefix(projectRoot, "/")
	projectRoot = filepath.ToSlash(filepath.Clean(filepath.FromSlash(projectRoot)))
	if projectRoot == "." || strings.HasPrefix(projectRoot, "../") {
		return "", fmt.Errorf("project root %q must be relative to workspace", projectRoot)
	}
	return filepath.Join(workspace, filepath.FromSlash(projectRoot)), nil
}

func readProjectConventionFile(name string) (string, bool, error) {
	raw, err := os.ReadFile(name)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read project convention file %s: %w", name, err)
	}

	contents := strings.TrimSpace(string(raw))
	if contents == "" {
		return "", false, nil
	}
	return contents, true, nil
}

func limitProjectConventions(contents string) string {
	contents = strings.TrimSpace(contents)
	if len(contents) <= MaxProjectConventionsBytes {
		return contents
	}
	return strings.TrimSpace(contents[:MaxProjectConventionsBytes])
}
