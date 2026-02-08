package config

import (
	"testing"
)

// TestWorkspaceRC_GetRunnerConfigForType tests per-runner-type config lookup.
func TestWorkspaceRC_GetRunnerConfigForType(t *testing.T) {
	rc := &WorkspaceRC{
		RestrictedRunners: map[string]*WorkspaceRunnerConfig{
			"exec": {
				Restrictions: &RunnerRestrictions{
					AllowReadFolders: []string{"/workspace"},
				},
			},
			"sandbox-exec": {
				Restrictions: &RunnerRestrictions{
					AllowReadFolders: []string{"/workspace", "/tmp"},
				},
			},
			"docker": {
				Restrictions: &RunnerRestrictions{
					Docker: &DockerRestrictions{
						Image: "alpine:latest",
					},
				},
			},
		},
	}

	// Test exec config
	execConfig := rc.GetRunnerConfigForType("exec")
	if execConfig == nil {
		t.Fatal("Expected exec config, got nil")
	}
	if len(execConfig.Restrictions.AllowReadFolders) != 1 {
		t.Errorf("Expected 1 allow_read_folder, got %d", len(execConfig.Restrictions.AllowReadFolders))
	}

	// Test sandbox-exec config
	sandboxConfig := rc.GetRunnerConfigForType("sandbox-exec")
	if sandboxConfig == nil {
		t.Fatal("Expected sandbox-exec config, got nil")
	}
	if len(sandboxConfig.Restrictions.AllowReadFolders) != 2 {
		t.Errorf("Expected 2 allow_read_folders, got %d", len(sandboxConfig.Restrictions.AllowReadFolders))
	}

	// Test docker config
	dockerConfig := rc.GetRunnerConfigForType("docker")
	if dockerConfig == nil {
		t.Fatal("Expected docker config, got nil")
	}
	if dockerConfig.Restrictions.Docker == nil || dockerConfig.Restrictions.Docker.Image != "alpine:latest" {
		t.Error("Expected docker image 'alpine:latest'")
	}

	// Test non-existent runner type (should return nil)
	unknownConfig := rc.GetRunnerConfigForType("firejail")
	if unknownConfig != nil {
		t.Errorf("Expected nil for unknown runner type, got %+v", unknownConfig)
	}
}

// TestWorkspaceRC_GetRunnerConfigForType_NoConfig tests behavior when runner type has no config.
func TestWorkspaceRC_GetRunnerConfigForType_NoConfig(t *testing.T) {
	rc := &WorkspaceRC{
		RestrictedRunners: map[string]*WorkspaceRunnerConfig{
			"exec": {
				Restrictions: &RunnerRestrictions{
					AllowReadFolders: []string{"/workspace"},
				},
			},
		},
	}

	// Test exec config (should exist)
	execConfig := rc.GetRunnerConfigForType("exec")
	if execConfig == nil {
		t.Fatal("Expected exec config, got nil")
	}
	if len(execConfig.Restrictions.AllowReadFolders) != 1 {
		t.Errorf("Expected 1 allow_read_folder, got %d", len(execConfig.Restrictions.AllowReadFolders))
	}

	// Test docker config (should be nil - no config for this runner type)
	dockerConfig := rc.GetRunnerConfigForType("docker")
	if dockerConfig != nil {
		t.Errorf("Expected nil for docker (no config), got %+v", dockerConfig)
	}
}

// TestParseWorkspaceRC_PerRunnerType tests parsing per-runner-type config from YAML.
func TestParseWorkspaceRC_PerRunnerType(t *testing.T) {
	yaml := `
restricted_runners:
  exec:
    restrictions:
      allow_read_folders:
        - "$WORKSPACE"
    merge_strategy: "extend"
  sandbox-exec:
    restrictions:
      allow_networking: false
      allow_read_folders:
        - "$WORKSPACE"
        - "/tmp"
    merge_strategy: "extend"
  docker:
    restrictions:
      allow_networking: false
      docker:
        image: "alpine:latest"
    merge_strategy: "replace"
`

	rc, err := parseWorkspaceRC([]byte(yaml))
	if err != nil {
		t.Fatalf("parseWorkspaceRC failed: %v", err)
	}

	if rc.RestrictedRunners == nil {
		t.Fatal("Expected RestrictedRunners, got nil")
	}

	if len(rc.RestrictedRunners) != 3 {
		t.Errorf("Expected 3 runner type configs, got %d", len(rc.RestrictedRunners))
	}

	// Check exec
	execCfg := rc.RestrictedRunners["exec"]
	if execCfg == nil {
		t.Fatal("Expected exec config, got nil")
	}
	if execCfg.Restrictions == nil {
		t.Fatal("Expected exec restrictions, got nil")
	}
	if len(execCfg.Restrictions.AllowReadFolders) != 1 {
		t.Errorf("Expected 1 allow_read_folder, got %d", len(execCfg.Restrictions.AllowReadFolders))
	}
	if execCfg.MergeStrategy != "extend" {
		t.Errorf("Expected exec merge_strategy 'extend', got '%s'", execCfg.MergeStrategy)
	}

	// Check sandbox-exec
	sandboxCfg := rc.RestrictedRunners["sandbox-exec"]
	if sandboxCfg == nil {
		t.Fatal("Expected sandbox-exec config, got nil")
	}
	if sandboxCfg.Restrictions == nil {
		t.Fatal("Expected sandbox-exec restrictions, got nil")
	}
	if sandboxCfg.Restrictions.AllowNetworking == nil || *sandboxCfg.Restrictions.AllowNetworking {
		t.Error("Expected sandbox-exec allow_networking=false")
	}
	if len(sandboxCfg.Restrictions.AllowReadFolders) != 2 {
		t.Errorf("Expected 2 allow_read_folders, got %d", len(sandboxCfg.Restrictions.AllowReadFolders))
	}

	// Check docker
	dockerCfg := rc.RestrictedRunners["docker"]
	if dockerCfg == nil {
		t.Fatal("Expected docker config, got nil")
	}
	if dockerCfg.Restrictions == nil || dockerCfg.Restrictions.Docker == nil {
		t.Fatal("Expected docker restrictions with docker config, got nil")
	}
	if dockerCfg.Restrictions.Docker.Image != "alpine:latest" {
		t.Errorf("Expected docker image 'alpine:latest', got '%s'", dockerCfg.Restrictions.Docker.Image)
	}
	if dockerCfg.MergeStrategy != "replace" {
		t.Errorf("Expected docker merge_strategy 'replace', got '%s'", dockerCfg.MergeStrategy)
	}
}
