package main

import (
	"testing"
)

func TestParseArgsDefaults(t *testing.T) {
	args := []string{}
	config, err := parseArgs(args)

	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}

	if config.Repo != "." {
		t.Errorf("Expected repo='.', got '%s'", config.Repo)
	}

	if config.Root != "algi-8bt" {
		t.Errorf("Expected root='algi-8bt', got '%s'", config.Root)
	}

	if config.Max != 0 {
		t.Errorf("Expected max=0, got %d", config.Max)
	}

	if config.DryRun != false {
		t.Errorf("Expected dry_run=false, got %t", config.DryRun)
	}

	if config.Model != "" {
		t.Errorf("Expected model='', got '%s'", config.Model)
	}
}
