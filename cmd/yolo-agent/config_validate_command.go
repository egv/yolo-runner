package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func defaultRunConfigValidateCommand(args []string) int {
	fs := flag.NewFlagSet("yolo-agent-config-validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: yolo-agent config validate [flags]")
	}

	repo := fs.String("repo", ".", "Repository root")
	profile := fs.String("profile", "", "Tracker profile name from .yolo-runner/config.yaml")
	root := fs.String("root", "", "Root task ID for scope validation")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	service := newTrackerConfigService()
	model, err := service.LoadModel(*repo)
	if err != nil {
		return reportInvalidConfig(err)
	}
	if _, err := resolveYoloAgentConfigDefaults(model.Agent); err != nil {
		return reportInvalidConfig(err)
	}

	profileName := resolveProfileSelectionPolicy(profileSelectionInput{
		FlagValue: *profile,
		EnvValue:  os.Getenv("YOLO_PROFILE"),
	})
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		profileName = strings.TrimSpace(model.DefaultProfile)
	}
	if profileName == "" {
		profileName = defaultProfileName
	}

	profileDef, ok := model.Profiles[profileName]
	if !ok {
		return reportInvalidConfig(fmt.Errorf("tracker profile %q not found (available: %s)", profileName, strings.Join(sortedProfileNames(model.Profiles), ", ")))
	}

	rootID := strings.TrimSpace(*root)
	if rootID == "" && profileDef.Tracker.TK != nil {
		scopeRoot := strings.TrimSpace(profileDef.Tracker.TK.Scope.Root)
		if scopeRoot != "" {
			rootID = scopeRoot
		}
	}

	if _, err := validateTrackerModel(profileName, profileDef.Tracker, rootID, os.Getenv); err != nil {
		return reportInvalidConfig(err)
	}

	fmt.Fprintln(os.Stdout, "config is valid")
	return 0
}

func reportInvalidConfig(err error) int {
	fmt.Fprintf(os.Stderr, "config is invalid: %v\n", err)
	return 1
}
