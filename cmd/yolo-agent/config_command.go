package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const starterTrackerConfigTemplate = "default_profile: default\nprofiles:\n  default:\n    tracker:\n      type: tk\n"

func defaultRunConfigInitCommand(args []string) int {
	fs := flag.NewFlagSet("yolo-agent config init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	repoRoot := fs.String("repo", ".", "Repository root")
	force := fs.Bool("force", false, "Overwrite existing .yolo-runner/config.yaml")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments for config init: %s\n", strings.Join(fs.Args(), " "))
		return 1
	}
	if err := writeStarterTrackerConfig(*repoRoot, *force); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "wrote %s\n", trackerConfigRelPath)
	return 0
}

func writeStarterTrackerConfig(repoRoot string, force bool) error {
	configPath := filepath.Join(repoRoot, trackerConfigRelPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("cannot create config directory for %s: %w", trackerConfigRelPath, err)
	}

	openFlags := os.O_CREATE | os.O_WRONLY | os.O_EXCL
	if force {
		openFlags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	file, err := os.OpenFile(configPath, openFlags, 0o644)
	if err != nil {
		if os.IsExist(err) && !force {
			return fmt.Errorf("config file at %s already exists; rerun with --force to overwrite", trackerConfigRelPath)
		}
		return fmt.Errorf("cannot write config file at %s: %w", trackerConfigRelPath, err)
	}
	defer func() {
		_ = file.Close()
	}()

	if _, err := file.WriteString(starterTrackerConfigTemplate); err != nil {
		return fmt.Errorf("cannot write config file at %s: %w", trackerConfigRelPath, err)
	}
	return nil
}
