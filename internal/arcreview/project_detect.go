package arcreview

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type ProjectContext struct {
	Root    string   `json:"root,omitempty"`
	Command []string `json:"command,omitempty"`
}

func DetectProjectContext(workspace string, files []PRChangedFile) (ProjectContext, error) {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		if path := strings.TrimSpace(file.Path); path != "" {
			paths = append(paths, path)
			continue
		}
		if oldPath := strings.TrimSpace(file.OldPath); oldPath != "" {
			paths = append(paths, oldPath)
		}
	}
	return DetectProjectContextFromPaths(workspace, paths)
}

func DetectProjectContextFromPaths(workspace string, paths []string) (ProjectContext, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ProjectContext{}, fmt.Errorf("workspace is required")
	}

	counts := map[string]int{}
	for _, changedPath := range paths {
		root, ok, err := nearestYaMakeRoot(workspace, changedPath)
		if err != nil {
			return ProjectContext{}, err
		}
		if ok {
			counts[root]++
		}
	}

	root, ok := dominantProjectRoot(counts)
	if !ok {
		return ProjectContext{}, fmt.Errorf("no ya.make project root found for changed files")
	}
	return ProjectContext{
		Root:    root,
		Command: []string{"ya", "make", "-t", root},
	}, nil
}

func nearestYaMakeRoot(workspace string, changedPath string) (string, bool, error) {
	changedPath = cleanProjectDetectPath(changedPath)
	if changedPath == "" {
		return "", false, nil
	}

	dir := path.Dir(changedPath)
	for {
		if ok, err := projectDetectFileExists(filepath.Join(workspace, filepath.FromSlash(dir), "ya.make")); err != nil {
			return "", false, err
		} else if ok {
			return dir, true, nil
		}
		if dir == "." {
			return "", false, nil
		}
		dir = path.Dir(dir)
	}
}

func cleanProjectDetectPath(changedPath string) string {
	changedPath = strings.TrimSpace(filepath.ToSlash(changedPath))
	if changedPath == "" {
		return ""
	}
	changedPath = strings.TrimPrefix(changedPath, "a/")
	changedPath = strings.TrimPrefix(changedPath, "b/")
	changedPath = strings.TrimPrefix(changedPath, "/")
	changedPath = path.Clean(changedPath)
	if changedPath == "." || strings.HasPrefix(changedPath, "../") {
		return ""
	}
	return changedPath
}

func projectDetectFileExists(name string) (bool, error) {
	info, err := os.Stat(name)
	if err == nil {
		return !info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func dominantProjectRoot(counts map[string]int) (string, bool) {
	if len(counts) == 0 {
		return "", false
	}

	roots := make([]string, 0, len(counts))
	for root := range counts {
		roots = append(roots, root)
	}
	sort.Slice(roots, func(i, j int) bool {
		left := roots[i]
		right := roots[j]
		if counts[left] != counts[right] {
			return counts[left] > counts[right]
		}
		if projectDetectDepth(left) != projectDetectDepth(right) {
			return projectDetectDepth(left) > projectDetectDepth(right)
		}
		return left < right
	})
	return roots[0], true
}

func projectDetectDepth(root string) int {
	if root == "." {
		return 0
	}
	return strings.Count(root, "/") + 1
}
