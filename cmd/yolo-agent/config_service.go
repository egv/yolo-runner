package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// trackerConfigService centralizes config loading and semantic validation.
type trackerConfigService struct {
	repoRoot  string
	getenv    func(string) string
	loadModel func(string) (trackerProfilesModel, error)

	loadOnce sync.Once
	model    trackerProfilesModel
	loadErr  error
}

func newTrackerConfigService(repoRoot string, getenv func(string) string) *trackerConfigService {
	return &trackerConfigService{
		repoRoot:  repoRoot,
		getenv:    getenv,
		loadModel: loadTrackerProfilesModel,
	}
}

func (s *trackerConfigService) loadModelOnce() (trackerProfilesModel, error) {
	s.loadOnce.Do(func() {
		configPath := filepath.Join(s.repoRoot, trackerConfigRelPath)
		s.model, s.loadErr = s.loadModel(configPath)
	})
	if s.loadErr != nil {
		return trackerProfilesModel{}, s.loadErr
	}
	return s.model, nil
}

func (s *trackerConfigService) loadAgentDefaults() (yoloAgentConfigDefaults, error) {
	model, err := s.loadModelOnce()
	if err != nil {
		return yoloAgentConfigDefaults{}, err
	}
	return resolveYoloAgentConfigDefaults(model.Agent)
}

func (s *trackerConfigService) resolveTrackerProfile(selectedProfile string, rootID string) (resolvedTrackerProfile, error) {
	model, err := s.loadModelOnce()
	if err != nil {
		return resolvedTrackerProfile{}, err
	}

	profileName := strings.TrimSpace(selectedProfile)
	if profileName == "" {
		profileName = strings.TrimSpace(model.DefaultProfile)
	}
	if profileName == "" {
		profileName = defaultProfileName
	}

	profile, ok := model.Profiles[profileName]
	if !ok {
		return resolvedTrackerProfile{}, fmt.Errorf("tracker profile %q not found (available: %s)", profileName, strings.Join(sortedProfileNames(model.Profiles), ", "))
	}

	validated, err := validateTrackerModel(profileName, profile.Tracker, rootID, s.getenv)
	if err != nil {
		return resolvedTrackerProfile{}, err
	}
	return resolvedTrackerProfile{
		Name:    profileName,
		Tracker: validated,
	}, nil
}
