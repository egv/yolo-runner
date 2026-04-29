package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type releaseWorkflow struct {
	Jobs map[string]releaseWorkflowJob `yaml:"jobs"`
}

type releaseWorkflowJob struct {
	Strategy struct {
		Matrix map[string]interface{} `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []releaseWorkflowStep `yaml:"steps"`
}

type releaseWorkflowStep struct {
	Name string            `yaml:"name"`
	Run  string            `yaml:"run"`
	Uses string            `yaml:"uses"`
	With map[string]string `yaml:"with"`
}

func TestReleaseWorkflowDefinesBuildMatrixAndChecksumArtifacts(t *testing.T) {
	workflow := readRepoFile(t, ".github", "workflows", "release.yml")

	var parsed releaseWorkflow
	if err := yaml.Unmarshal([]byte(workflow), &parsed); err != nil {
		t.Fatalf("unmarshal workflow YAML: %v", err)
	}

	job := findReleaseMatrixJob(t, parsed)
	matrixEntries := releaseMatrixEntries(job.Strategy.Matrix)
	if len(matrixEntries) == 0 {
		t.Fatalf("release workflow must define strategy.matrix.include entries")
	}

	seenArtifactNames := map[string]struct{}{}
	for _, entry := range matrixEntries {
		artifact, ok := entry["artifact"]
		if !ok || strings.TrimSpace(artifact) == "" {
			t.Fatalf("release matrix entry missing artifact name: %#v", entry)
		}
		seenArtifactNames[artifact] = struct{}{}
	}

	expectedArtifacts := releaseArtifactsFromInstallMatrix(t)
	for artifact := range expectedArtifacts {
		if _, ok := seenArtifactNames[artifact]; !ok {
			t.Fatalf("release matrix missing supported artifact %q", artifact)
		}
	}
	for artifact := range seenArtifactNames {
		if _, ok := expectedArtifacts[artifact]; !ok {
			t.Fatalf("release matrix defines unsupported artifact %q", artifact)
		}
	}

	runContent := ""
	for _, step := range job.Steps {
		runContent += step.Run
		runContent += " "
	}
	if !strings.Contains(strings.ToLower(runContent), "sha256sum") {
		t.Fatalf("release workflow missing checksum generation (expected sha256sum usage)")
	}
	if !strings.Contains(runContent, "checksums-${{ matrix.artifact }}.txt") {
		t.Fatalf("release workflow missing checksum artifact generation (expected checksums-${{ matrix.artifact }}.txt)")
	}
	setupGoStepFound := false
	goVersionFileSet := false
	for _, step := range job.Steps {
		if !strings.Contains(step.Uses, "actions/setup-go@") {
			continue
		}
		setupGoStepFound = true
		if step.With["go-version-file"] == "go.mod" {
			goVersionFileSet = true
		}
	}
	if !setupGoStepFound {
		t.Fatalf("release workflow missing setup-go step")
	}
	if !goVersionFileSet {
		t.Fatalf("release workflow setup-go step must set go-version-file: go.mod")
	}
}

func TestReleaseWorkflowContainsSupportedPlatformsInArtifactNaming(t *testing.T) {
	workflow := readRepoFile(t, ".github", "workflows", "release.yml")
	expectedArtifacts := releaseArtifactsFromInstallMatrix(t)

	for artifact := range expectedArtifacts {
		if !strings.Contains(workflow, artifact) {
			t.Fatalf("release workflow missing artifact naming for matrix entry %q", artifact)
		}
	}

	unsupported := "yolo-runner_windows_arm64"
	if strings.Contains(workflow, unsupported) {
		t.Fatalf("release workflow includes unsupported matrix artifact %q", unsupported)
	}
}

func TestReleaseWorkflowDefinesCrossPlatformBuildAndPublishSteps(t *testing.T) {
	workflow := readRepoFile(t, ".github", "workflows", "release.yml")

	if !strings.Contains(workflow, "actions/checkout@v4") {
		t.Fatalf("release workflow missing checkout step")
	}
	if !strings.Contains(workflow, "actions/setup-go@") {
		t.Fatalf("release workflow missing setup-go step")
	}

	var parsed releaseWorkflow
	if err := yaml.Unmarshal([]byte(workflow), &parsed); err != nil {
		t.Fatalf("unmarshal workflow YAML: %v", err)
	}
	job := findReleaseMatrixJob(t, parsed)

	buildStepFound := false
	publishStepFound := false
	packageStepFound := false
	checksumStepFound := false

	for _, step := range job.Steps {
		run := strings.ToLower(step.Run)
		if !buildStepFound && strings.Contains(run, "go build") && strings.Contains(run, "${{ matrix.os }}") && strings.Contains(run, "${{ matrix.arch }}") {
			buildStepFound = true
		}
		if !packageStepFound && strings.Contains(run, "${{ matrix.artifact }}") &&
			(strings.Contains(run, "tar -czf") || strings.Contains(run, "zip")) {
			packageStepFound = true
		}
		if !checksumStepFound && strings.Contains(run, "sha256sum") && strings.Contains(run, "checksums-${{ matrix.artifact }}.txt") {
			checksumStepFound = true
		}
		if strings.Contains(step.Uses, "softprops/action-gh-release") {
			files, ok := step.With["files"]
			if !ok {
				t.Fatalf("release workflow publish step missing files input")
			}
			if !strings.Contains(files, "dist/${{ matrix.artifact }}") {
				t.Fatalf("release workflow publish step must include matrix artifact")
			}
			if !strings.Contains(files, "dist/checksums-${{ matrix.artifact }}.txt") {
				t.Fatalf("release workflow publish step must include matching checksum artifact path")
			}
			publishStepFound = true
		}
	}

	if !buildStepFound {
		t.Fatalf("release workflow missing matrix-aware go build step")
	}
	if !packageStepFound {
		t.Fatalf("release workflow missing matrix artifact packaging step")
	}
	if !checksumStepFound {
		t.Fatalf("release workflow missing checksum generation step for checksums-${{ matrix.artifact }}.txt")
	}
	if !publishStepFound {
		t.Fatalf("release workflow missing artifact publish step using softprops/action-gh-release")
	}
}

func TestReleaseWorkflowUsesChecksumFilenamePerArtifact(t *testing.T) {
	workflow := readRepoFile(t, ".github", "workflows", "release.yml")

	if !strings.Contains(workflow, "dist/checksums-${{ matrix.artifact }}.txt") {
		t.Fatalf("release workflow must use per-artifact checksum filename with matrix artifact placeholder")
	}
	if strings.Contains(workflow, "dist/checksums.txt") {
		t.Fatalf("release workflow must not use shared checksum filename dist/checksums.txt in matrix release jobs")
	}
}

func TestReleaseWorkflowSerializesMatrixUploadsAndValidatesArchiveContents(t *testing.T) {
	workflow := readRepoFile(t, ".github", "workflows", "release.yml")

	if !strings.Contains(workflow, "max-parallel: 1") {
		t.Fatalf("release workflow must serialize matrix jobs to avoid release publish retries")
	}

	requiredSnippets := []string{
		`if [[ ! -s "dist/${{ matrix.artifact }}" ]]; then`,
		`unzip -l "dist/${{ matrix.artifact }}" > dist/archive-contents.txt`,
		`tar -tzf "dist/${{ matrix.artifact }}" > dist/archive-contents.txt`,
		`missing expected binary in zip`,
		`missing expected binary in tarball`,
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(workflow, snippet) {
			t.Fatalf("release workflow missing archive validation snippet %q", snippet)
		}
	}
}

func TestReleaseWorkflowBuildsToolchainBundleWithVersionMetadata(t *testing.T) {
	workflow := readRepoFile(t, ".github", "workflows", "release.yml")
	binaries := releaseToolchainBinariesFromMakefile(t)

	var parsed releaseWorkflow
	if err := yaml.Unmarshal([]byte(workflow), &parsed); err != nil {
		t.Fatalf("unmarshal workflow YAML: %v", err)
	}
	job := findReleaseMatrixJob(t, parsed)

	buildStepRun := ""
	packageStepRun := ""
	for _, step := range job.Steps {
		if strings.Contains(step.Run, "go build") {
			buildStepRun = step.Run
		}
		if strings.Contains(step.Run, "${{ matrix.artifact }}") && (strings.Contains(step.Run, "tar -czf") || strings.Contains(step.Run, "zip -j")) {
			packageStepRun = strings.TrimSpace(step.Run)
		}
	}
	if buildStepRun == "" {
		t.Fatalf("release workflow missing build run step")
	}

	buildBinaries := releaseWorkflowBinariesFromStepRun(buildStepRun)
	for _, binary := range buildBinaries {
		if _, err := os.Stat(filepath.Join("..", "..", "cmd", binary)); err != nil {
			t.Fatalf("release workflow includes binary %q without tracked cmd package: %v", binary, err)
		}
	}
	for _, binary := range binaries {
		found := false
		for _, listed := range buildBinaries {
			if binary == listed {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("release workflow build step does not include toolchain binary %q", binary)
		}
	}
	if !strings.Contains(buildStepRun, "-ldflags") {
		t.Fatalf("release workflow build step must provide ldflags")
	}
	if !strings.Contains(buildStepRun, "-X github.com/egv/yolo-runner/v2/internal/version.Version=${{ github.ref_name }}") {
		t.Fatalf("release workflow build step must inject shared version metadata from github ref name")
	}
	if packageStepRun == "" {
		t.Fatalf("release workflow missing packaging/checksum step")
	}
	if !strings.Contains(packageStepRun, "binaries=\"") {
		t.Fatalf("release workflow packaging step is expected to declare packaged binary list")
	}
	if !strings.Contains(packageStepRun, "for binary in") {
		t.Fatalf("release workflow packaging step is expected to iterate packaged binaries")
	}
	if !strings.Contains(packageStepRun, "${built[@]}") {
		t.Fatalf("release workflow packaging step must package all built binaries from the same list")
	}
}

func findReleaseMatrixJob(t *testing.T, wf releaseWorkflow) releaseWorkflowJob {
	t.Helper()
	for _, job := range wf.Jobs {
		if _, ok := job.Strategy.Matrix["os"]; ok {
			return job
		}
		if _, ok := job.Strategy.Matrix["include"]; ok {
			return job
		}
	}
	t.Fatalf("release workflow missing strategy.matrix")
	return releaseWorkflowJob{}
}

func releaseMatrixEntries(matrix map[string]interface{}) []map[string]string {
	rawInclude, ok := matrix["include"]
	if !ok {
		return nil
	}

	include, ok := rawInclude.([]interface{})
	if !ok {
		return nil
	}

	entries := make([]map[string]string, 0, len(include))
	for _, rawEntry := range include {
		entry, ok := rawEntry.(map[string]interface{})
		if !ok {
			continue
		}

		parsed := map[string]string{}
		for key, rawValue := range entry {
			value, ok := rawValue.(string)
			if !ok {
				continue
			}
			parsed[key] = value
		}
		entries = append(entries, parsed)
	}

	return entries
}

func releaseArtifactsFromInstallMatrix(t *testing.T) map[string]struct{} {
	t.Helper()

	matrix := readRepoFile(t, "docs", "install-matrix.md")
	artifacts := map[string]struct{}{}

	for _, line := range strings.Split(matrix, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(strings.ToLower(line), "platform") || strings.Contains(line, "| ---") {
			continue
		}

		cells := splitMarkdownRow(line)
		if len(cells) < 2 {
			continue
		}

		platform := strings.TrimSpace(cells[0])
		arch := strings.TrimSpace(cells[1])
		if arch == "" {
			continue
		}

		artifactOS, ok := releaseArtifactOS(platform)
		if !ok {
			continue
		}

		artifactSuffix := "tar.gz"
		if artifactOS == "windows" {
			artifactSuffix = "zip"
		}

		artifact := fmt.Sprintf("yolo-runner_%s_%s.%s", artifactOS, arch, artifactSuffix)
		artifacts[artifact] = struct{}{}
	}

	if len(artifacts) == 0 {
		t.Fatalf("no release artifacts derived from install matrix")
	}

	return artifacts
}

func releaseToolchainBinariesFromMakefile(t *testing.T) []string {
	t.Helper()

	makefile := readRepoFile(t, "Makefile")
	seen := map[string]struct{}{}
	ordered := []string{}

	for _, line := range strings.Split(makefile, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "go build -o bin/") {
			continue
		}

		parts := strings.Fields(line)
		binaryPath := ""
		for i := 0; i < len(parts)-1; i++ {
			if parts[i] == "-o" && strings.HasPrefix(parts[i+1], "bin/") {
				binaryPath = parts[i+1]
				break
			}
		}
		if binaryPath == "" {
			continue
		}

		binary := strings.TrimPrefix(binaryPath, "bin/")
		if binary == "" {
			continue
		}
		if _, exists := seen[binary]; exists {
			continue
		}
		seen[binary] = struct{}{}
		ordered = append(ordered, binary)
	}

	if len(ordered) == 0 {
		t.Fatalf("no release binaries found in Makefile build targets")
	}
	return ordered
}

func releaseWorkflowBinariesFromStepRun(stepRun string) []string {
	for _, line := range strings.Split(stepRun, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "binaries=") {
			binariesLiteral := strings.TrimPrefix(line, "binaries=")
			binariesLiteral = strings.Trim(binariesLiteral, "\"")
			binariesLiteral = strings.TrimSpace(binariesLiteral)
			if binariesLiteral == "" {
				continue
			}
			return strings.Fields(binariesLiteral)
		}
	}
	return nil
}

func releaseArtifactOS(platform string) (string, bool) {
	switch strings.ToLower(platform) {
	case "macos":
		return "darwin", true
	case "linux":
		return "linux", true
	case "windows":
		return "windows", true
	default:
		return "", false
	}
}
