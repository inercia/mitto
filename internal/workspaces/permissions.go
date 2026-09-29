package workspaces

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// PermissionMode selects how a conversation's permission requests (running
// commands, writing files outside the workspace, etc.) are resolved.
//
// PermissionMode is a schema-only concept in this increment: it defines the
// config surface (types, defaults, validation, legacy migration) but does not
// yet wire into the runtime enforcement/consumption sites (CEL context
// building, session manager construction, settings-save seeding). Those are
// deferred to a follow-up.
type PermissionMode string

const (
	// PermissionModeUnset means "no explicit mode configured at this level" —
	// the effective mode is inherited from the next broader scope (server,
	// then global). Never persisted as an explicit choice; it is the zero
	// value of PermissionMode.
	PermissionModeUnset PermissionMode = ""

	// PermissionModeAsk means the agent must always prompt for permission.
	PermissionModeAsk PermissionMode = "ask"

	// PermissionModeAllow means permission requests are auto-approved. This
	// is the legacy behavior of AutoApprove=true.
	PermissionModeAllow PermissionMode = "allow"

	// PermissionModeSystemOne means permission decisions are delegated to an
	// external "System One" HTTP safety-check endpoint (see SystemOneConfig).
	PermissionModeSystemOne PermissionMode = "system_one"
)

// ValidPermissionModes is the list of all valid explicit PermissionMode
// values. PermissionModeUnset is intentionally excluded — it is a valid
// *zero value* (meaning "inherit"), not a value a caller should explicitly
// set, so it is validated separately wherever emptiness is meaningful.
var ValidPermissionModes = []PermissionMode{
	PermissionModeAsk,
	PermissionModeAllow,
	PermissionModeSystemOne,
}

// IsValidPermissionMode reports whether mode is PermissionModeUnset (i.e.
// "inherit") or one of ValidPermissionModes.
func IsValidPermissionMode(mode PermissionMode) bool {
	if mode == PermissionModeUnset {
		return true
	}
	for _, m := range ValidPermissionModes {
		if mode == m {
			return true
		}
	}
	return false
}

// SystemOneConfig configures the external "System One" safety-check endpoint
// used when PermissionMode is PermissionModeSystemOne. The endpoint receives
// a request (built from PayloadTemplate) describing the pending permission
// request and returns a JSON response; ResultField is looked up in that
// response to decide whether to allow or deny.
//
// Payload-template placeholder substitution (e.g. how the pending operation's
// details are injected into PayloadTemplate) is not implemented in this
// schema-only increment — PayloadTemplate is validated for non-emptiness
// only; the substitution engine is deferred to the enforcement follow-up.
type SystemOneConfig struct {
	// Endpoint is the HTTPS (or http://127.0.0.1 / http://localhost loopback)
	// URL of the System One safety-check service.
	Endpoint string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`

	// PayloadTemplate is the JSON request body template sent to Endpoint.
	// Must be non-empty; placeholder substitution is validated structurally
	// only (see package doc comment above).
	PayloadTemplate string `json:"payload_template,omitempty" yaml:"payload_template,omitempty"`

	// ResultField is the field name to read from the JSON response to decide
	// allow/deny (e.g. "is_safe"). Must match ^[A-Za-z0-9_-]+$.
	ResultField string `json:"result_field,omitempty" yaml:"result_field,omitempty"`

	// Threshold is the minimum numeric value of ResultField (when numeric)
	// required to allow the operation. Must be in [0, 1].
	Threshold float64 `json:"threshold,omitempty" yaml:"threshold,omitempty"`

	// Timeout bounds how long to wait for the System One endpoint to
	// respond. Must be in [1s, 60s].
	Timeout time.Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`

	// OnDeny selects the fallback behavior when System One denies the
	// operation: "ask" (prompt the user) or "reject" (deny outright). Must be
	// one of SystemOneFallbackAsk / SystemOneFallbackReject.
	OnDeny SystemOneFallback `json:"on_deny,omitempty" yaml:"on_deny,omitempty"`

	// OnError selects the fallback behavior when the System One request
	// itself fails (network error, timeout, malformed response): "ask" or
	// "reject". Must be one of SystemOneFallbackAsk / SystemOneFallbackReject.
	OnError SystemOneFallback `json:"on_error,omitempty" yaml:"on_error,omitempty"`
}

// SystemOneFallback selects what happens when a System One safety check
// denies an operation, or fails to complete.
type SystemOneFallback string

const (
	// SystemOneFallbackAsk falls back to prompting the user for permission.
	SystemOneFallbackAsk SystemOneFallback = "ask"
	// SystemOneFallbackReject denies the operation outright, with no prompt.
	SystemOneFallbackReject SystemOneFallback = "reject"
)

// ValidSystemOneFallbacks is the list of valid SystemOneFallback values.
var ValidSystemOneFallbacks = []SystemOneFallback{
	SystemOneFallbackAsk,
	SystemOneFallbackReject,
}

func isValidSystemOneFallback(v SystemOneFallback) bool {
	for _, f := range ValidSystemOneFallbacks {
		if v == f {
			return true
		}
	}
	return false
}

// PermissionSettings groups a PermissionMode with its optional SystemOne
// configuration. It is embedded at multiple scopes (workspace, ACP server,
// global) that all resolve independently and are combined by callers via
// their own inheritance logic (not implemented in this schema-only
// increment).
type PermissionSettings struct {
	// Mode selects how permission requests are resolved at this scope.
	// PermissionModeUnset ("") means "inherit from the next broader scope".
	Mode PermissionMode `json:"mode,omitempty" yaml:"mode,omitempty"`

	// SystemOne configures the external safety-check endpoint. Only
	// meaningful (and only validated) when Mode == PermissionModeSystemOne.
	SystemOne *SystemOneConfig `json:"system_one,omitempty" yaml:"system_one,omitempty"`
}

// resultFieldPattern validates SystemOneConfig.ResultField.
var resultFieldPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// DefaultSystemOneConfig returns the built-in default SystemOneConfig used
// when a caller enables PermissionModeSystemOne without specifying a full
// configuration.
func DefaultSystemOneConfig() SystemOneConfig {
	return SystemOneConfig{
		Endpoint:        "https://api.typesafe.ai/v1/systemone",
		PayloadTemplate: `{"action":"{{.Action}}","command":"{{.Command}}"}`,
		ResultField:     "is_safe",
		Threshold:       0.5,
		Timeout:         10 * time.Second,
		OnDeny:          SystemOneFallbackAsk,
		OnError:         SystemOneFallbackAsk,
	}
}

// ValidateSystemOneConfig validates a SystemOneConfig's fields. It is only
// meaningful to call when the owning PermissionSettings.Mode is
// PermissionModeSystemOne.
func ValidateSystemOneConfig(cfg *SystemOneConfig) error {
	if cfg == nil {
		return fmt.Errorf("system_one config is required when mode is %q", PermissionModeSystemOne)
	}
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return fmt.Errorf("system_one.endpoint must not be empty")
	}
	if err := validateSystemOneEndpoint(cfg.Endpoint); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.PayloadTemplate) == "" {
		return fmt.Errorf("system_one.payload_template must not be empty")
	}
	if strings.TrimSpace(cfg.ResultField) == "" {
		return fmt.Errorf("system_one.result_field must not be empty")
	}
	if !resultFieldPattern.MatchString(cfg.ResultField) {
		return fmt.Errorf("system_one.result_field %q must match %s", cfg.ResultField, resultFieldPattern.String())
	}
	if cfg.Threshold < 0 || cfg.Threshold > 1 {
		return fmt.Errorf("system_one.threshold must be in [0, 1], got %v", cfg.Threshold)
	}
	if cfg.Timeout < time.Second || cfg.Timeout > 60*time.Second {
		return fmt.Errorf("system_one.timeout must be in [1s, 60s], got %v", cfg.Timeout)
	}
	if !isValidSystemOneFallback(cfg.OnDeny) {
		return fmt.Errorf("system_one.on_deny must be one of %v, got %q", ValidSystemOneFallbacks, cfg.OnDeny)
	}
	if !isValidSystemOneFallback(cfg.OnError) {
		return fmt.Errorf("system_one.on_error must be one of %v, got %q", ValidSystemOneFallbacks, cfg.OnError)
	}
	return nil
}

// validateSystemOneEndpoint requires an https:// URL, or an http:// URL
// targeting a loopback host (127.0.0.1 or localhost) for local development.
func validateSystemOneEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("system_one.endpoint is not a valid URL: %w", err)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "127.0.0.1" || host == "localhost" || host == "::1" {
			return nil
		}
		return fmt.Errorf("system_one.endpoint using http:// must target a loopback host (127.0.0.1/localhost), got %q", host)
	default:
		return fmt.Errorf("system_one.endpoint must use https:// (or http:// for loopback), got scheme %q", u.Scheme)
	}
}

// ValidatePermissionSettings validates a PermissionSettings value. A nil
// receiver or a PermissionSettings with Mode == PermissionModeUnset is
// always valid (nothing configured at this scope).
func ValidatePermissionSettings(p *PermissionSettings) error {
	if p == nil || p.Mode == PermissionModeUnset {
		return nil
	}
	if !IsValidPermissionMode(p.Mode) {
		return fmt.Errorf("permissions.mode must be one of %v, got %q", ValidPermissionModes, p.Mode)
	}
	if p.Mode == PermissionModeSystemOne {
		if err := ValidateSystemOneConfig(p.SystemOne); err != nil {
			return err
		}
	}
	return nil
}

// EffectiveMode returns p.Mode, or fallback when p is nil or p.Mode is
// PermissionModeUnset. This is the additive inheritance helper new callers
// should use; it deliberately does not replace or alter the existing
// AutoApprove-based helpers (e.g. WorkspaceSettings.GetAutoApprove,
// PermissionsConfig.IsAutoApprove), which keep their current behavior for
// not-yet-migrated consumers.
func (p *PermissionSettings) EffectiveMode(fallback PermissionMode) PermissionMode {
	if p == nil || p.Mode == PermissionModeUnset {
		return fallback
	}
	return p.Mode
}

// MigrateLegacyAutoApprove maps a legacy *bool AutoApprove value to a
// PermissionMode, per the scope-specific rules from the design plan:
//
//   - autoApprove == nil (absent) at workspace/ACP-server scope -> inherit ("")
//   - autoApprove == nil (absent) at global scope                -> allow
//     (preserves PermissionsConfig.IsAutoApprove()'s current true-default)
//   - *autoApprove == true                                       -> allow
//   - *autoApprove == false at workspace/ACP-server scope         -> inherit ("")
//   - *autoApprove == false at global scope                       -> ask
//
// isGlobal selects which absent/false mapping applies.
func MigrateLegacyAutoApprove(autoApprove *bool, isGlobal bool) PermissionMode {
	if autoApprove == nil {
		if isGlobal {
			return PermissionModeAllow
		}
		return PermissionModeUnset
	}
	if *autoApprove {
		return PermissionModeAllow
	}
	if isGlobal {
		return PermissionModeAsk
	}
	return PermissionModeUnset
}
