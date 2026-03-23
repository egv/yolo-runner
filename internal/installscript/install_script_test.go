package installscript

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallScriptPlanModeReportsPlatformAndInstallCoordinates(t *testing.T) {
	t.Parallel()
	repoRoot := testRepoRoot(t)

	tmpHome := t.TempDir()
	tmpInstallDir := filepath.Join(tmpHome, ".local", "bin")
	releaseBase := "https://example.invalid/releases/latest"

	tests := []struct {
		name         string
		osInput      string
		archInput    string
		wantPlatform string
		wantArch     string
		wantArtifact string
		wantChecksum string
		wantBinary   string
		wantInstall  string
	}{
		{
			name:         "linux amd64",
			osInput:      "Linux",
			archInput:    "x86_64",
			wantPlatform: "linux",
			wantArch:     "amd64",
			wantArtifact: "yolo-runner_linux_amd64.tar.gz",
			wantBinary:   "yolo-runner",
			wantChecksum: "checksums-yolo-runner_linux_amd64.tar.gz.txt",
			wantInstall:  filepath.Join(tmpInstallDir, "yolo-runner"),
		},
		{
			name:         "darwin arm64",
			osInput:      "Darwin",
			archInput:    "arm64",
			wantPlatform: "darwin",
			wantArch:     "arm64",
			wantArtifact: "yolo-runner_darwin_arm64.tar.gz",
			wantBinary:   "yolo-runner",
			wantChecksum: "checksums-yolo-runner_darwin_arm64.tar.gz.txt",
			wantInstall:  filepath.Join(tmpInstallDir, "yolo-runner"),
		},
		{
			name:         "windows amd64",
			osInput:      "Windows",
			archInput:    "amd64",
			wantPlatform: "windows",
			wantArch:     "amd64",
			wantArtifact: "yolo-runner_windows_amd64.zip",
			wantBinary:   "yolo-runner.exe",
			wantChecksum: "checksums-yolo-runner_windows_amd64.zip.txt",
			wantInstall:  filepath.Join(tmpInstallDir, "yolo-runner.exe"),
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := runInstallScript(repoRoot, []string{
				"--plan",
				"--os", tc.osInput,
				"--arch", tc.archInput,
				"--release-base", releaseBase,
				"--install-dir", tmpInstallDir,
			}, tmpHome)
			if err != nil {
				t.Fatalf("run install.sh --plan: %v\noutput: %s", err, out)
			}

			plan := parsePlanOutput(out)
			gotPlatform, ok := plan["platform"]
			if !ok {
				t.Fatalf("missing platform in plan output: %s", out)
			}
			if gotPlatform != tc.wantPlatform {
				t.Fatalf("platform = %q, want %q", gotPlatform, tc.wantPlatform)
			}

			gotArch := plan["arch"]
			if gotArch != tc.wantArch {
				t.Fatalf("arch = %q, want %q", gotArch, tc.wantArch)
			}

			gotArtifact := plan["artifact"]
			if gotArtifact != tc.wantArtifact {
				t.Fatalf("artifact = %q, want %q", gotArtifact, tc.wantArtifact)
			}

			wantURL := releaseBase + "/" + tc.wantArtifact
			if gotURL := plan["artifact_url"]; gotURL != wantURL {
				t.Fatalf("artifact_url = %q, want %q", gotURL, wantURL)
			}

			wantChecksumURL := releaseBase + "/" + tc.wantChecksum
			if gotChecksum := plan["checksum_url"]; gotChecksum != wantChecksumURL {
				t.Fatalf("checksum_url = %q, want %q", gotChecksum, wantChecksumURL)
			}

			if gotBinary := plan["binary"]; gotBinary != tc.wantBinary {
				t.Fatalf("binary = %q, want %q", gotBinary, tc.wantBinary)
			}

			if gotInstallPath := plan["install_path"]; gotInstallPath != tc.wantInstall {
				t.Fatalf("install_path = %q, want %q", gotInstallPath, tc.wantInstall)
			}
		})
	}
}

func TestInstallScriptPlanModeUsesEgvDefaultReleaseBase(t *testing.T) {
	repoRoot := testRepoRoot(t)

	out, err := runInstallScript(repoRoot, []string{"--plan", "--os", "Linux", "--arch", "amd64"}, t.TempDir())
	if err != nil {
		t.Fatalf("run install.sh --plan: %v\noutput: %s", err, out)
	}

	plan := parsePlanOutput(out)
	wantArtifactURL := "https://github.com/egv/yolo-runner/releases/latest/download/yolo-runner_linux_amd64.tar.gz"
	wantChecksumURL := "https://github.com/egv/yolo-runner/releases/latest/download/checksums-yolo-runner_linux_amd64.tar.gz.txt"

	if got := plan["artifact_url"]; got != wantArtifactURL {
		t.Fatalf("artifact_url = %q, want %q", got, wantArtifactURL)
	}
	if got := plan["checksum_url"]; got != wantChecksumURL {
		t.Fatalf("checksum_url = %q, want %q", got, wantChecksumURL)
	}

}

func TestInstallScriptRejectsUnsupportedPlatforms(t *testing.T) {
	t.Parallel()
	repoRoot := testRepoRoot(t)

	_, err := runInstallScript(repoRoot, []string{"--plan", "--os", "Plan9", "--arch", "amd64"}, t.TempDir())
	if err == nil {
		t.Fatal("expected plan mode to fail for unsupported OS")
	}
}

func TestInstallScriptInstallModeUsesExpectedBinaryAndInstallPath(t *testing.T) {
	repoRoot := testRepoRoot(t)

	tmpHome := t.TempDir()
	tmpArtifacts := t.TempDir()
	tmpLocalAppData := filepath.Join(tmpArtifacts, "localappdata")
	releaseBase := "https://example.invalid/releases/latest"

	tests := []struct {
		name        string
		osInput     string
		archInput   string
		artifact    string
		binaryName  string
		installPath string
	}{
		{
			name:        "linux amd64",
			osInput:     "Linux",
			archInput:   "x86_64",
			artifact:    "yolo-runner_linux_amd64.tar.gz",
			binaryName:  "yolo-runner",
			installPath: filepath.Join(tmpHome, ".local", "bin", "yolo-runner"),
		},
		{
			name:        "darwin arm64",
			osInput:     "Darwin",
			archInput:   "arm64",
			artifact:    "yolo-runner_darwin_arm64.tar.gz",
			binaryName:  "yolo-runner",
			installPath: filepath.Join(tmpHome, ".local", "bin", "yolo-runner"),
		},
		{
			name:        "windows amd64",
			osInput:     "Windows",
			archInput:   "amd64",
			artifact:    "yolo-runner_windows_amd64.zip",
			binaryName:  "yolo-runner.exe",
			installPath: filepath.Join(tmpLocalAppData, "yolo-runner", "bin", "yolo-runner.exe"),
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			artifactURL := releaseBase + "/" + tc.artifact
			checksumFilename := "checksums-" + tc.artifact + ".txt"
			checksumURL := releaseBase + "/" + checksumFilename

			var artifactPath string
			switch filepath.Ext(tc.artifact) {
			case ".zip":
				artifactPath = writeArtifactZipOne(t, tmpArtifacts, tc.artifact, tc.binaryName, []byte("#!/bin/sh\necho ok\n"))
			default:
				artifactPath = writeArtifactTarGzOne(t, tmpArtifacts, tc.artifact, tc.binaryName, []byte("#!/bin/sh\necho ok\n"))
			}
			artifactData, err := os.ReadFile(artifactPath)
			if err != nil {
				t.Fatalf("read artifact fixture: %v", err)
			}
			sum := sha256.Sum256(artifactData)
			checksumPath := filepath.Join(tmpArtifacts, checksumFilename)
			if err := os.WriteFile(
				checksumPath,
				[]byte(fmt.Sprintf("%s  dist/%s\n", hex.EncodeToString(sum[:]), tc.artifact)),
				0o600,
			); err != nil {
				t.Fatalf("write checksum fixture: %v", err)
			}

			fakeCurlLog := filepath.Join(tmpArtifacts, tc.name+".curl.calls")
			fakeCurlDir := filepath.Join(tmpArtifacts, tc.name, "bin")
			writeFakeCurl(t, fakeCurlDir, fakeCurlLog)

			cmdEnv := []string{
				"PATH=" + fakeCurlDir + ":" + os.Getenv("PATH"),
				"YOLO_FAKE_ARTIFACT_PATH=" + artifactPath,
				"YOLO_FAKE_CHECKSUM_PATH=" + checksumPath,
				"YOLO_FAKE_ARTIFACT_URL=" + artifactURL,
				"YOLO_FAKE_CHECKSUM_URL=" + checksumURL,
				"YOLO_FAKE_CURL_LOG=" + fakeCurlLog,
			}
			if tc.osInput == "Windows" {
				cmdEnv = append(cmdEnv, "LOCALAPPDATA="+tmpLocalAppData)
			}

			out, err := runInstallScript(
				repoRoot,
				[]string{
					"--os", tc.osInput,
					"--arch", tc.archInput,
					"--release-base", releaseBase,
				},
				tmpHome,
				cmdEnv...,
			)
			if err != nil {
				t.Fatalf("install should succeed: %v\noutput: %s", err, out)
			}

			calls := strings.Split(strings.TrimSpace(readFileString(t, fakeCurlLog)), "\n")
			if len(calls) != 2 {
				t.Fatalf("expected two downloads, got %d\ncalls: %q", len(calls), calls)
			}
			if calls[0] != artifactURL {
				t.Fatalf("first download = %q, want %q", calls[0], artifactURL)
			}
			if calls[1] != checksumURL {
				t.Fatalf("second download = %q, want %q", calls[1], checksumURL)
			}

			if _, err := os.Stat(tc.installPath); err != nil {
				t.Fatalf("expected installed binary at %s: %v", tc.installPath, err)
			}
			if _, err := os.Stat(filepath.Dir(tc.installPath)); err != nil {
				t.Fatalf("expected install directory %s to exist: %v", filepath.Dir(tc.installPath), err)
			}
			if info, err := os.Stat(tc.installPath); err == nil && info.Mode()&0o111 == 0 {
				t.Fatalf("installed file is not executable: %s", tc.installPath)
			}
			if !strings.Contains(out, "installed "+tc.binaryName+" to "+filepath.Dir(tc.installPath)) {
				t.Fatalf("install output missing resolved target path:\n%s", out)
			}
		})
	}
}

func TestInstallScriptInstallModeInstallsAllBinariesFromToolchain(t *testing.T) {
	repoRoot := testRepoRoot(t)

	tmpHome := t.TempDir()
	tmpArtifacts := t.TempDir()
	releaseBase := "https://example.invalid/releases/latest"

	binaries := []string{
		"yolo-runner",
		"yolo-agent",
		"yolo-task",
		"yolo-tui",
		"yolo-linear-webhook",
		"yolo-linear-worker",
	}

	artifactName := "yolo-runner_linux_amd64.tar.gz"
	artifactURL := releaseBase + "/" + artifactName
	checksumFilename := "checksums-" + artifactName + ".txt"
	checksumURL := releaseBase + "/" + checksumFilename

	binaryContents := []byte("#!/bin/sh\necho clean-bin\n")
	entries := map[string][]byte{}
	for _, binaryName := range binaries {
		entries[binaryName] = binaryContents
	}

	artifactPath := writeArtifactTarGz(t, tmpArtifacts, artifactName, entries)
	artifactData, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read artifact fixture: %v", err)
	}
	sum := sha256.Sum256(artifactData)
	checksumPath := filepath.Join(tmpArtifacts, checksumFilename)
	if err := os.WriteFile(
		checksumPath,
		[]byte(fmt.Sprintf("%s  dist/%s\n", hex.EncodeToString(sum[:]), artifactName)),
		0o600,
	); err != nil {
		t.Fatalf("write checksum fixture: %v", err)
	}

	fakeCurlLog := filepath.Join(tmpArtifacts, "multi-binary.curl.calls")
	fakeCurlDir := filepath.Join(tmpArtifacts, "bin")
	writeFakeCurl(t, fakeCurlDir, fakeCurlLog)

	fakeCurlVars := []string{
		"PATH=" + fakeCurlDir + ":" + os.Getenv("PATH"),
		"YOLO_FAKE_ARTIFACT_PATH=" + artifactPath,
		"YOLO_FAKE_CHECKSUM_PATH=" + checksumPath,
		"YOLO_FAKE_ARTIFACT_URL=" + artifactURL,
		"YOLO_FAKE_CHECKSUM_URL=" + checksumURL,
		"YOLO_FAKE_CURL_LOG=" + fakeCurlLog,
	}

	out, err := runInstallScript(
		repoRoot,
		[]string{
			"--os", "Linux",
			"--arch", "x86_64",
			"--release-base", releaseBase,
		},
		tmpHome,
		fakeCurlVars...,
	)
	if err != nil {
		t.Fatalf("install should succeed: %v\noutput: %s", err, out)
	}

	calls := strings.Split(strings.TrimSpace(readFileString(t, fakeCurlLog)), "\n")
	if len(calls) != 2 {
		t.Fatalf("expected two downloads, got %d\ncalls: %q", len(calls), calls)
	}
	if calls[0] != artifactURL {
		t.Fatalf("first download = %q, want %q", calls[0], artifactURL)
	}
	if calls[1] != checksumURL {
		t.Fatalf("second download = %q, want %q", calls[1], checksumURL)
	}

	installDir := filepath.Join(tmpHome, ".local", "bin")
	for _, binary := range binaries {
		targetPath := filepath.Join(installDir, binary)
		info, err := os.Stat(targetPath)
		if err != nil {
			t.Fatalf("expected installed binary at %s: %v", targetPath, err)
		}
		if info.Mode()&0o111 == 0 {
			t.Fatalf("installed file is not executable: %s", targetPath)
		}
		if !strings.Contains(out, "installed "+binary+" to "+installDir) {
			t.Fatalf("install output missing resolved install message for %s:\n%s", binary, out)
		}
	}
}

func TestInstallScriptInstallModeFailsOnChecksumMismatch(t *testing.T) {
	repoRoot := testRepoRoot(t)

	tmpHome := t.TempDir()
	tmpArtifacts := t.TempDir()
	releaseBase := "https://example.invalid/releases/latest"
	artifactName := "yolo-runner_linux_amd64.tar.gz"
	artifactURL := releaseBase + "/" + artifactName
	checksumFilename := "checksums-" + artifactName + ".txt"
	checksumURL := releaseBase + "/" + checksumFilename

	artifactPath := writeArtifactTarGzOne(t, tmpArtifacts, artifactName, "yolo-runner", []byte("#!/bin/sh\necho ok\n"))
	checksumPath := filepath.Join(tmpArtifacts, checksumFilename)
	if err := os.WriteFile(checksumPath, []byte(fmt.Sprintf("%064s  %s\n", "0", "dist/"+artifactName)), 0o600); err != nil {
		t.Fatalf("write checksum fixture: %v", err)
	}

	fakeCurlLog := filepath.Join(tmpArtifacts, "curl.calls")
	fakeCurlDir := filepath.Join(tmpArtifacts, "bin")
	installDir := filepath.Join(tmpHome, ".local", "bin")
	writeFakeCurl(t, fakeCurlDir, fakeCurlLog)
	_, err := runInstallScript(
		repoRoot,
		[]string{
			"--os", "Linux",
			"--arch", "x86_64",
			"--release-base", releaseBase,
		},
		tmpHome,
		"PATH="+fakeCurlDir+":"+os.Getenv("PATH"),
		"YOLO_FAKE_ARTIFACT_PATH="+artifactPath,
		"YOLO_FAKE_CHECKSUM_PATH="+checksumPath,
		"YOLO_FAKE_ARTIFACT_URL="+artifactURL,
		"YOLO_FAKE_CHECKSUM_URL="+checksumURL,
		"YOLO_FAKE_CURL_LOG="+fakeCurlLog,
	)
	if err == nil {
		t.Fatal("expected checksum mismatch to fail install")
	}
	if _, err := os.Stat(filepath.Join(installDir, "yolo-runner")); err == nil {
		t.Fatal("did not expect installed binary when checksum mismatch")
	}
}

func TestInstallScriptInstallModeInstallsRunnableBinaryForCleanEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("clean environment install execution is validated on Unix-family environments only")
	}

	repoRoot := testRepoRoot(t)

	repoHome := t.TempDir()
	tmpArtifacts := t.TempDir()
	releaseBase := "https://example.invalid/releases/latest"

	osInput := "Linux"
	archInput := "amd64"
	artifactName := "yolo-runner_linux_amd64.tar.gz"
	if runtime.GOARCH == "arm64" {
		archInput = "arm64"
		artifactName = "yolo-runner_linux_arm64.tar.gz"
	}
	if runtime.GOOS == "darwin" {
		osInput = "Darwin"
		artifactName = "yolo-runner_darwin_" + archInput + ".tar.gz"
	}

	artifactURL := releaseBase + "/" + artifactName
	checksumFilename := "checksums-" + artifactName + ".txt"
	checksumURL := releaseBase + "/" + checksumFilename

	artifactPath := writeArtifactTarGzOne(
		t,
		tmpArtifacts,
		artifactName,
		"yolo-runner",
		[]byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n\techo clean-install-version\n\texit 0\nfi\n"),
	)
	artifactData, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read artifact fixture: %v", err)
	}
	sum := sha256.Sum256(artifactData)
	checksumPath := filepath.Join(tmpArtifacts, checksumFilename)
	if err := os.WriteFile(
		checksumPath,
		[]byte(fmt.Sprintf("%s  dist/%s\n", hex.EncodeToString(sum[:]), artifactName)),
		0o600,
	); err != nil {
		t.Fatalf("write checksum fixture: %v", err)
	}

	fakeCurlLog := filepath.Join(tmpArtifacts, "clean.curl.calls")
	fakeCurlDir := filepath.Join(tmpArtifacts, "bin")
	writeFakeCurl(t, fakeCurlDir, fakeCurlLog)

	_, err = runInstallScript(
		repoRoot,
		[]string{
			"--os", osInput,
			"--arch", archInput,
			"--release-base", releaseBase,
		},
		repoHome,
		"PATH="+fakeCurlDir+":"+os.Getenv("PATH"),
		"YOLO_FAKE_ARTIFACT_PATH="+artifactPath,
		"YOLO_FAKE_CHECKSUM_PATH="+checksumPath,
		"YOLO_FAKE_ARTIFACT_URL="+artifactURL,
		"YOLO_FAKE_CHECKSUM_URL="+checksumURL,
		"YOLO_FAKE_CURL_LOG="+fakeCurlLog,
	)
	if err != nil {
		installOutput, _ := os.ReadFile(fakeCurlLog)
		t.Fatalf("install should succeed: %v\ncurl log: %s", err, installOutput)
	}

	installedPath := filepath.Join(repoHome, ".local", "bin", "yolo-runner")
	_, err = os.Stat(installedPath)
	if err != nil {
		t.Fatalf("expected installed binary at %s: %v", installedPath, err)
	}

	versionOutput, err := exec.Command(installedPath, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("installed binary --version should execute successfully: %v (%s)", err, versionOutput)
	}
	if strings.TrimSpace(string(versionOutput)) != "clean-install-version" {
		t.Fatalf("unexpected version output: %q", strings.TrimSpace(string(versionOutput)))
	}
}

func TestInstallScriptChecksumVerification(t *testing.T) {
	repoRoot := testRepoRoot(t)

	tmpDir := t.TempDir()
	artifact := filepath.Join(tmpDir, "artifact.bin")
	manifest := filepath.Join(tmpDir, "artifact.bin.sha256")

	artifactContents := "verified-download"
	if err := os.WriteFile(artifact, []byte(artifactContents), 0o600); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	sum := sha256.Sum256([]byte(artifactContents))
	hash := hex.EncodeToString(sum[:])
	manifestEntry := fmt.Sprintf("%s  %s\n", hash, filepath.Join("dist", filepath.Base(artifact)))
	if err := os.WriteFile(manifest, []byte(manifestEntry), 0o600); err != nil {
		t.Fatalf("write checksum manifest: %v", err)
	}

	if out, err := runInstallScript(repoRoot, []string{"--test-checksum", artifact, manifest}, tmpDir); err != nil {
		t.Fatalf("expected checksum to verify, got error: %v\nout: %s", err, out)
	}

	manifestEntry = fmt.Sprintf("%s  %s\n", strings.Repeat("0", len(hash)), filepath.Join("dist", filepath.Base(artifact)))
	if err := os.WriteFile(manifest, []byte(manifestEntry), 0o600); err != nil {
		t.Fatalf("rewrite checksum manifest: %v", err)
	}

	if out, err := runInstallScript(repoRoot, []string{"--test-checksum", artifact, manifest}, tmpDir); err == nil {
		t.Fatalf("expected checksum mismatch to fail\noutput: %s", out)
	}
}

func runInstallScript(repoRoot string, args []string, homeDir string, extraEnv ...string) (string, error) {
	cmd := exec.Command("bash", append([]string{filepath.Join(repoRoot, "install.sh")}, args...)...)
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}

func writeArtifactTarGz(t *testing.T, dir, filename string, entries map[string][]byte) string {
	t.Helper()

	artifactPath := filepath.Join(dir, filename)
	artifact, err := os.Create(artifactPath)
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	defer artifact.Close()

	gw, err := gzip.NewWriterLevel(artifact, gzip.BestCompression)
	if err != nil {
		t.Fatalf("create gzip writer: %v", err)
	}
	tw := tar.NewWriter(gw)

	for binaryName, binaryContents := range entries {
		header := &tar.Header{
			Name: binaryName,
			Mode: 0o755,
			Size: int64(len(binaryContents)),
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write(binaryContents); err != nil {
			t.Fatalf("write tar content: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	return artifactPath
}

func writeArtifactTarGzOne(t *testing.T, dir, filename, binaryName string, binaryContents []byte) string {
	t.Helper()

	return writeArtifactTarGz(t, dir, filename, map[string][]byte{binaryName: binaryContents})
}

func writeArtifactZip(t *testing.T, dir, filename string, entries map[string][]byte) string {
	t.Helper()

	artifactPath := filepath.Join(dir, filename)
	artifact, err := os.Create(artifactPath)
	if err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	defer artifact.Close()

	zw := zip.NewWriter(artifact)
	for binaryName, binaryContents := range entries {
		header := &zip.FileHeader{
			Name:   binaryName,
			Method: zip.Deflate,
		}
		header.SetMode(0o755)
		writer, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatalf("create zip header: %v", err)
		}
		if _, err := writer.Write(binaryContents); err != nil {
			t.Fatalf("write zip content: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	return artifactPath
}

func writeArtifactZipOne(t *testing.T, dir, filename, binaryName string, binaryContents []byte) string {
	t.Helper()

	return writeArtifactZip(t, dir, filename, map[string][]byte{binaryName: binaryContents})
}

func writeFakeCurl(t *testing.T, dir string, callLog string) string {
	t.Helper()

	scriptPath := filepath.Join(dir, "curl")
	script := []byte(`#!/usr/bin/env bash
set -euo pipefail

url=""
output=""
while [[ "$#" -gt 0 ]]; do
	case "$1" in
		-o)
			shift
			output="$1"
			;;
		-*)
			;;
		*)
			url="$1"
			;;
	esac
	shift
done

if [[ -z "$url" || -z "$output" ]]; then
	echo "fake curl: invalid arguments" >&2
	exit 1
fi

if [[ -z "${YOLO_FAKE_ARTIFACT_PATH:-}" || -z "${YOLO_FAKE_CHECKSUM_PATH:-}" || -z "${YOLO_FAKE_ARTIFACT_URL:-}" || -z "${YOLO_FAKE_CHECKSUM_URL:-}" || -z "${YOLO_FAKE_CURL_LOG:-}" ]]; then
	echo "fake curl: missing env vars" >&2
	exit 1
fi

echo "$url" >> "$YOLO_FAKE_CURL_LOG"
if [[ "$url" == "$YOLO_FAKE_ARTIFACT_URL" ]]; then
	cp "$YOLO_FAKE_ARTIFACT_PATH" "$output"
	exit 0
fi

if [[ "$url" == "$YOLO_FAKE_CHECKSUM_URL" ]]; then
	cp "$YOLO_FAKE_CHECKSUM_PATH" "$output"
	exit 0
fi

echo "unexpected download URL: $url" >&2
exit 1
`)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create fake curl dir: %v", err)
	}
	if err := os.WriteFile(scriptPath, script, 0o755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}
	return scriptPath
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return string(b)
}

func parsePlanOutput(output string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) == 2 {
			result[parts[0]] = parts[1]
		}
	}
	return result
}

func testRepoRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}

	root := filepath.Join(wd, "..", "..")
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return root
}
