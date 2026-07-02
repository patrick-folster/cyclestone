package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrick-folster/cyclestone/internal/config"
)

func TestGenerateDefaultConfigStartsWithoutMilestones(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	configPath := filepath.Join(root, ".cyclestone", "milestone.yml")

	if err := config.GenerateDefaultConfig(configPath); err != nil {
		t.Fatalf("GenerateDefaultConfig failed: %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Milestones) != 0 {
		t.Fatalf("expected no default milestones, got %d", len(cfg.Milestones))
	}

	if _, err := os.Stat(filepath.Join(root, ".cyclestone", "milestones")); !os.IsNotExist(err) {
		t.Fatalf("expected no milestones directory, stat error: %v", err)
	}
}

func TestIsConfigMissing(t *testing.T) {
	t.Run("config already exists", func(t *testing.T) {
		root := t.TempDir()
		configPath := filepath.Join(root, ".cyclestone", "milestone.yml")
		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configPath, []byte("milestones: []\n"), 0644); err != nil {
			t.Fatal(err)
		}

		missing, err := isConfigMissing(configPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if missing {
			t.Fatal("expected missing to be false")
		}
	})

	t.Run("missing config", func(t *testing.T) {
		root := t.TempDir()
		configPath := filepath.Join(root, ".cyclestone", "milestone.yml")

		missing, err := isConfigMissing(configPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !missing {
			t.Fatal("expected missing to be true")
		}
	})
}

func TestMissingConfigNonInteractiveErrorMentionsSetupRequirement(t *testing.T) {
	msg := missingConfigNonInteractiveError()
	for _, want := range []string{"milestones configuration not found", "interactive terminal", "existing config"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected non-interactive error to mention %q, got %q", want, msg)
		}
	}
}
