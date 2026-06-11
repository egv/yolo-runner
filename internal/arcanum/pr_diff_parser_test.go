package arcanum

import (
	"reflect"
	"testing"

	"github.com/egv/yolo-runner/v2/internal/arcreview"
)

func TestParsePRChangedFilesDiffMapsArcChangesFixture(t *testing.T) {
	fixture := []byte(`diff --git a/internal/arcreview/review_prompt.go b/internal/arcreview/review_prompt.go
index 5f61b5a..a2dd52c 100644
--- a/internal/arcreview/review_prompt.go
+++ b/internal/arcreview/review_prompt.go
@@ -62,6 +62,7 @@ func reviewRevisionDiffsSection(files []PRChangedFile) string {
 		lines = append(lines,
 			"File: "+reviewPromptFallback(file.Path),
 			"Old path: "+reviewPromptFallback(file.OldPath),
+			"Status: "+reviewPromptFallback(file.Status),
 			fmt.Sprintf("Additions: %d", file.Additions),
 			fmt.Sprintf("Deletions: %d", file.Deletions),
 			"Diff:",
diff --git a/internal/arcanum/pr_diff_parser.go b/internal/arcanum/pr_diff_parser.go
new file mode 100644
index 0000000..6e2de71
--- /dev/null
+++ b/internal/arcanum/pr_diff_parser.go
@@ -0,0 +1,5 @@
+package arcanum
+
+func ParsePRChangedFilesDiff(data []byte) {
+	panic("not implemented")
+}
`)

	got, err := ParsePRChangedFilesDiff(fixture)
	if err != nil {
		t.Fatalf("ParsePRChangedFilesDiff() error = %v", err)
	}

	want := []arcreview.PRChangedFile{
		{
			Path: "internal/arcreview/review_prompt.go",
			Diff: `diff --git a/internal/arcreview/review_prompt.go b/internal/arcreview/review_prompt.go
index 5f61b5a..a2dd52c 100644
--- a/internal/arcreview/review_prompt.go
+++ b/internal/arcreview/review_prompt.go
@@ -62,6 +62,7 @@ func reviewRevisionDiffsSection(files []PRChangedFile) string {
 		lines = append(lines,
 			"File: "+reviewPromptFallback(file.Path),
 			"Old path: "+reviewPromptFallback(file.OldPath),
+			"Status: "+reviewPromptFallback(file.Status),
 			fmt.Sprintf("Additions: %d", file.Additions),
 			fmt.Sprintf("Deletions: %d", file.Deletions),
 			"Diff:",
`,
		},
		{
			Path: "internal/arcanum/pr_diff_parser.go",
			Diff: `diff --git a/internal/arcanum/pr_diff_parser.go b/internal/arcanum/pr_diff_parser.go
new file mode 100644
index 0000000..6e2de71
--- /dev/null
+++ b/internal/arcanum/pr_diff_parser.go
@@ -0,0 +1,5 @@
+package arcanum
+
+func ParsePRChangedFilesDiff(data []byte) {
+	panic("not implemented")
+}
`,
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParsePRChangedFilesDiff() = %#v, want %#v", got, want)
	}
}
