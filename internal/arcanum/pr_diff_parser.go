package arcanum

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

// ParsePRChangedFilesDiff parses unified diff output emitted by `arc pr changes [PR]`.
func ParsePRChangedFilesDiff(data []byte) ([]arcreview.PRChangedFile, error) {
	var files []arcreview.PRChangedFile
	var current arcreview.PRChangedFile
	var diff strings.Builder
	inFile := false

	flush := func() error {
		if !inFile {
			return nil
		}
		if strings.TrimSpace(current.Path) == "" {
			return fmt.Errorf("parse arc pr changes diff: file section missing path")
		}
		current.Diff = diff.String()
		files = append(files, current)
		return nil
	}

	for _, line := range strings.SplitAfter(string(data), "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			if err := flush(); err != nil {
				return nil, err
			}
			current = arcreview.PRChangedFile{Path: prDiffGitPath(line)}
			diff.Reset()
			inFile = true
		}
		if !inFile {
			continue
		}
		diff.WriteString(line)
	}

	if err := flush(); err != nil {
		return nil, err
	}
	return files, nil
}

func prDiffGitPath(line string) string {
	fields := prDiffGitHeaderFields(strings.TrimSpace(strings.TrimPrefix(line, "diff --git ")))
	if len(fields) < 2 {
		return ""
	}
	return cleanPRDiffPath(fields[1])
}

func prDiffGitHeaderFields(value string) []string {
	fields := make([]string, 0, 2)
	for strings.TrimSpace(value) != "" {
		value = strings.TrimLeft(value, " \t")
		if strings.HasPrefix(value, `"`) {
			quoted, rest := prDiffQuotedField(value)
			fields = append(fields, quoted)
			value = rest
			continue
		}

		next := strings.IndexAny(value, " \t")
		if next == -1 {
			fields = append(fields, value)
			break
		}
		fields = append(fields, value[:next])
		value = value[next+1:]
	}
	return fields
}

func prDiffQuotedField(value string) (string, string) {
	for i := 1; i < len(value); i++ {
		if value[i] != '"' || value[i-1] == '\\' {
			continue
		}
		unquoted, err := strconv.Unquote(value[:i+1])
		if err != nil {
			return value[:i+1], value[i+1:]
		}
		return unquoted, value[i+1:]
	}
	return value, ""
}

func cleanPRDiffPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "/dev/null" {
		return ""
	}
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	return path
}
