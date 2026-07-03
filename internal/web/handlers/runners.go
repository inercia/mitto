package handlers

import (
	"net/http"
	"runtime"

	configPkg "github.com/inercia/mitto/internal/config"
	"github.com/inercia/mitto/internal/runner"
)

// RunnerInfo contains information about a runner type.
type RunnerInfo struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Supported   bool   `json:"supported"`
	Warning     string `json:"warning,omitempty"`
}

// HandleSupportedRunners handles GET /api/supported-runners.
// Returns a list of runner types with their support status on the current platform.
func (h *Handlers) HandleSupportedRunners(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	runners := []RunnerInfo{
		{
			Type:        "exec",
			Label:       "exec (no restrictions)",
			Description: "No sandboxing - runs with full system access",
			Supported:   true,
		},
		{
			Type:        "sandbox-exec",
			Label:       "sandbox-exec (macOS)",
			Description: "macOS native sandboxing",
			Supported:   runtime.GOOS == "darwin",
			Warning:     CheckRunnerSupport("sandbox-exec"),
		},
		{
			Type:        "firejail",
			Label:       "firejail (Linux)",
			Description: "Linux sandboxing with firejail",
			Supported:   runtime.GOOS == "linux",
			Warning:     CheckRunnerSupport("firejail"),
		},
		{
			Type:        "docker",
			Label:       "docker (all platforms)",
			Description: "Docker container sandboxing",
			Supported:   true, // Available on all platforms if Docker is installed
			Warning:     CheckRunnerSupport("docker"),
		},
	}

	writeJSONOK(w, runners)
}

// CheckRunnerSupport checks if a runner type is supported on the current platform.
// Returns a warning message if the runner may not work, or empty string if it should work.
func CheckRunnerSupport(runnerType string) string {
	switch runnerType {
	case "sandbox-exec":
		if runtime.GOOS != "darwin" {
			return "sandbox-exec is only available on macOS"
		}
	case "firejail":
		if runtime.GOOS != "linux" {
			return "firejail is only available on Linux"
		}
	case "docker":
		// Try to create a temporary runner to check if docker is available
		// This is a lightweight check - the actual runner creation will do full validation
		testRunner, err := runner.NewRunner(nil, nil, map[string]*configPkg.WorkspaceRunnerConfig{
			"docker": {
				Type: "docker",
				Restrictions: &configPkg.RunnerRestrictions{
					Docker: &configPkg.DockerRestrictions{
						Image: "alpine:latest",
					},
				},
			},
		}, "", nil)
		if err != nil {
			return "Docker may not be available: " + err.Error()
		}
		if testRunner != nil && testRunner.Type() == "exec" {
			// Fallback occurred
			return "Docker is not available on this system"
		}
	}
	return ""
}
