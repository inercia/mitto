package slackbridge

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// Environment variable names for the runtime-only Slack bridge configuration
// (mitto-qewp PoC). Deliberately NOT surfaced in Settings/UI and NEVER
// persisted to disk — see docs/devel/slack-bridge.md.
const (
	EnvAppToken        = "MITTO_SLACK_APP_TOKEN"         // xapp-... (Socket Mode)
	EnvBotToken        = "MITTO_SLACK_BOT_TOKEN"         // xoxb-...
	EnvTeamID          = "MITTO_SLACK_TEAM_ID"           // Slack workspace/team ID
	EnvChannelID       = "MITTO_SLACK_CHANNEL_ID"        // Slack channel ID to listen on
	EnvTargetSessionID = "MITTO_SLACK_TARGET_SESSION_ID" // Mitto conversation ID to trigger
)

// Config holds the runtime-only configuration for the Slack event source.
// Never persisted; sourced exclusively from environment variables at process
// start via LoadConfigFromEnv.
type Config struct {
	AppToken        string
	BotToken        string
	TeamID          string
	ChannelID       string
	TargetSessionID string
}

// EnvironmentStatus is the value-free view exposed to migration clients.
// It deliberately contains no credential fields or configured values.
type EnvironmentStatus struct {
	Present          bool     `json:"present"`
	Complete         bool     `json:"complete"`
	MissingVariables []string `json:"missing_variables"`
	TeamID           string   `json:"team_id,omitempty"`
	ChannelID        string   `json:"channel_id,omitempty"`
	TargetSessionID  string   `json:"target_session_id,omitempty"`
	Active           bool     `json:"active"`
	Shadowed         bool     `json:"shadowed"`
}

// requiredEnvVars lists every env var LoadConfigFromEnv requires, in a fixed
// order used both for "all absent" detection and for the missing-vars error.
var requiredEnvVars = []string{EnvAppToken, EnvBotToken, EnvTeamID, EnvChannelID, EnvTargetSessionID}

// LoadConfigFromEnv reads the Slack bridge configuration from the process
// environment. Three outcomes:
//
//   - All required vars absent: (zero Config, false, nil) — the feature is
//     simply disabled, not an error.
//   - Some but not all present: (zero Config, false, err) — a clear,
//     value-free error naming only the missing variable NAMES (never any
//     configured value, satisfying the "never log tokens" requirement) so a
//     partial/malformed configuration fails safely instead of silently
//     running with an incomplete filter.
//   - All present (and non-blank after trimming): (Config, true, nil).
func LoadConfigFromEnv() (Config, bool, error) {
	cfg, status := InspectEnvironment()
	if !status.Present {
		return Config{}, false, nil
	}
	if !status.Complete {
		return Config{}, false, fmt.Errorf(
			"slackbridge: incomplete configuration, missing environment variable(s): %s",
			strings.Join(status.MissingVariables, ", "),
		)
	}
	return cfg, true, nil
}

// InspectEnvironment reads the legacy configuration once and returns both the
// backend-only values and a safe status view suitable for REST/UI responses.
func InspectEnvironment() (Config, EnvironmentStatus) {
	values := make(map[string]string, len(requiredEnvVars))
	present := 0
	var missing []string
	for _, name := range requiredEnvVars {
		v := strings.TrimSpace(os.Getenv(name))
		values[name] = v
		if v == "" {
			missing = append(missing, name)
		} else {
			present++
		}
	}

	sort.Strings(missing)
	cfg := Config{
		AppToken:        values[EnvAppToken],
		BotToken:        values[EnvBotToken],
		TeamID:          values[EnvTeamID],
		ChannelID:       values[EnvChannelID],
		TargetSessionID: values[EnvTargetSessionID],
	}
	return cfg, EnvironmentStatus{
		Present: present > 0, Complete: present == len(requiredEnvVars), MissingVariables: missing,
		TeamID: cfg.TeamID, ChannelID: cfg.ChannelID, TargetSessionID: cfg.TargetSessionID,
	}
}
