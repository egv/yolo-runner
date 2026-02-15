package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func loadTrackerProfilesModel(path string) (trackerProfilesModel, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultTrackerProfilesModel(), nil
		}
		return trackerProfilesModel{}, fmt.Errorf("cannot read config file at %s: %w", trackerConfigRelPath, err)
	}

	var model trackerProfilesModel
	decoder := yaml.NewDecoder(strings.NewReader(string(content)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&model); err != nil {
		return trackerProfilesModel{}, fmt.Errorf("cannot parse config file at %s: %w", trackerConfigRelPath, err)
	}

	if len(model.Profiles) == 0 && strings.TrimSpace(model.Tracker.Type) != "" {
		model.Profiles = map[string]trackerProfileDef{
			defaultProfileName: {Tracker: model.Tracker},
		}
	}

	if len(model.Profiles) == 0 {
		return trackerProfilesModel{}, fmt.Errorf("config file at %s must define at least one profile", trackerConfigRelPath)
	}
	return model, nil
}

func defaultTrackerProfilesModel() trackerProfilesModel {
	return trackerProfilesModel{
		DefaultProfile: defaultProfileName,
		Profiles: map[string]trackerProfileDef{
			defaultProfileName: {
				Tracker: trackerModel{
					Type: trackerTypeTK,
				},
			},
		},
	}
}
