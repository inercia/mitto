package handlers

import (
	"net/http"
	"os"

	"github.com/inercia/mitto/internal/appdir"
	"github.com/inercia/mitto/internal/fileutil"
)

// UIPreferences represents the client-side UI state that needs to persist
// across app launches. This is stored server-side because the macOS app
// uses random ports, which means localStorage is isolated per launch.
type UIPreferences struct {
	// GroupingMode is the conversation list grouping mode: "none", "server", or "folder"
	GroupingMode string `json:"grouping_mode,omitempty"`

	// ExpandedGroups maps group keys to their expanded state (true = expanded)
	// Group keys are server names (for server grouping) or folder paths (for folder grouping)
	ExpandedGroups map[string]bool `json:"expanded_groups,omitempty"`

	// FilterTabGrouping maps filter tab IDs to their grouping mode
	// Each filter tab (conversations, loop, archived) can have its own grouping mode
	FilterTabGrouping map[string]string `json:"filter_tab_grouping,omitempty"`

	// PromptSortMode is the sorting mode for prompts in the dropdown: "alphabetical" or "color"
	// Default is "alphabetical" (sort by name within groups)
	// "color" sorts by color hue, then by name
	PromptSortMode string `json:"prompt_sort_mode,omitempty"`

	// FontSize is the UI font size class: "small" (default) or "large".
	// Persisted server-side because the macOS app uses random ports, so
	// localStorage — which is per-origin (scheme+host+port) — is reset on
	// every launch and the previously-chosen size would otherwise be lost.
	FontSize string `json:"font_size,omitempty"`

	// Theme is the explicit light/dark choice: "light" or "dark".
	// (Same server-side rationale as FontSize; see the FontSize comment
	// above — every field below shares it.)
	Theme string `json:"theme,omitempty"`

	// ThemeLight is the daisyUI theme name used in the light slot
	// (e.g. "mitto", "cupcake"). Only names from NAMED_THEMES are valid;
	// the frontend enforces the same allow-list, so the server rejects
	// values that are not letters/digits/dashes to avoid persisting junk.
	ThemeLight string `json:"theme_light,omitempty"`

	// ThemeDark is the daisyUI theme name used in the dark slot
	// (same value space as ThemeLight).
	ThemeDark string `json:"theme_dark,omitempty"`

	// FollowSystemTheme mirrors the "follow system theme" toggle.
	// Nullable so that "unset" (default: true) is distinguishable from
	// "explicitly false".
	FollowSystemTheme *bool `json:"follow_system_theme,omitempty"`

	// FollowSystemReducedMotion mirrors the "follow system reduced motion"
	// toggle. Nullable for the same reason as FollowSystemTheme.
	FollowSystemReducedMotion *bool `json:"follow_system_reduced_motion,omitempty"`

	// ReduceAnimations is the explicit "reduce animations" choice.
	// Nullable so we don't clobber the OS-driven default when the user
	// has never touched the toggle.
	ReduceAnimations *bool `json:"reduce_animations,omitempty"`

	// DashboardHiddenCharts is the list of canonical chart IDs the user has hidden
	// on the Dashboard's Activity strip. IDs absent from this list render normally,
	// so charts added in future versions default to visible and existing users are
	// not surprised by a hidden chart after upgrade. Empty list = all charts visible.
	DashboardHiddenCharts []string `json:"dashboard_hidden_charts,omitempty"`
}

// isValidThemeName reports whether s is an acceptable daisyUI theme name.
// The frontend's NAMED_THEMES enforces the real allow-list; the server just
// gates the character set so a rogue client can't stuff arbitrary bytes
// into the persisted preferences file. Letters, digits, and dashes only;
// bounded length keeps the check O(1).
func isValidThemeName(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 32 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}
	return true
}

// HandleUIPreferences handles GET and PUT /api/ui-preferences.
// GET returns the current UI preferences.
// PUT saves new UI preferences.
func (h *Handlers) HandleUIPreferences(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleGetUIPreferences(w, r)
	case http.MethodPut:
		h.handleSaveUIPreferences(w, r)
	default:
		methodNotAllowed(w)
	}
}

// handleGetUIPreferences handles GET /api/ui-preferences.
func (h *Handlers) handleGetUIPreferences(w http.ResponseWriter, r *http.Request) {
	prefs, err := loadUIPreferences()
	if err != nil {
		// If file doesn't exist, return empty preferences
		if os.IsNotExist(err) {
			writeJSONOK(w, UIPreferences{})
			return
		}
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to load UI preferences", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to load UI preferences")
		return
	}

	writeJSONOK(w, prefs)
}

// handleSaveUIPreferences handles PUT /api/ui-preferences.
func (h *Handlers) handleSaveUIPreferences(w http.ResponseWriter, r *http.Request) {
	var prefs UIPreferences
	if !parseJSONBody(w, r, &prefs) {
		return
	}

	// Validate grouping mode
	if prefs.GroupingMode != "" &&
		prefs.GroupingMode != "none" &&
		prefs.GroupingMode != "server" &&
		prefs.GroupingMode != "folder" &&
		prefs.GroupingMode != "workspace" {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid grouping_mode: must be 'none', 'server', 'folder', or 'workspace'")
		return
	}

	// Validate prompt sort mode
	if prefs.PromptSortMode != "" &&
		prefs.PromptSortMode != "alphabetical" &&
		prefs.PromptSortMode != "color" {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid prompt_sort_mode: must be 'alphabetical' or 'color'")
		return
	}

	// Validate font size
	if prefs.FontSize != "" &&
		prefs.FontSize != "small" &&
		prefs.FontSize != "large" {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid font_size: must be 'small' or 'large'")
		return
	}

	// Validate explicit theme mode
	if prefs.Theme != "" &&
		prefs.Theme != "light" &&
		prefs.Theme != "dark" {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid theme: must be 'light' or 'dark'")
		return
	}

	// Validate daisyUI theme slot names (character-set gate only; the
	// authoritative allow-list lives in the frontend NAMED_THEMES).
	if !isValidThemeName(prefs.ThemeLight) {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid theme_light: must be a short alphanumeric name")
		return
	}
	if !isValidThemeName(prefs.ThemeDark) {
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid theme_dark: must be a short alphanumeric name")
		return
	}

	// Validate filter_tab_grouping keys and values
	validFilterTabs := map[string]bool{"conversations": true, "loop": true, "archived": true}
	validGroupingModes := map[string]bool{"none": true, "server": true, "folder": true, "workspace": true}
	for key, value := range prefs.FilterTabGrouping {
		if !validFilterTabs[key] {
			writeErrorJSON(w, http.StatusBadRequest, "", "Invalid filter_tab_grouping key: must be 'conversations', 'loop', or 'archived'")
			return
		}
		if !validGroupingModes[value] {
			writeErrorJSON(w, http.StatusBadRequest, "", "Invalid filter_tab_grouping value: must be 'none', 'server', 'folder', or 'workspace'")
			return
		}
	}

	// Dashboard hidden-charts: strip unknown/duplicate IDs, then reject a payload
	// that would hide every known chart (Activity strip must always show at least
	// one card — matches the "cannot uncheck all" UX constraint from mitto-3i2).
	prefs.DashboardHiddenCharts = filterKnownChartIDs(prefs.DashboardHiddenCharts)
	if len(prefs.DashboardHiddenCharts) > 0 &&
		len(prefs.DashboardHiddenCharts) == len(KnownDashboardChartIDs) {
		writeErrorJSON(w, http.StatusBadRequest, "at_least_one_chart_must_be_visible",
			"At least one dashboard chart must remain visible")
		return
	}

	if err := saveUIPreferences(&prefs); err != nil {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("Failed to save UI preferences", "error", err)
		}
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to save UI preferences")
		return
	}

	if h.deps.Logger != nil {
		h.deps.Logger.Debug("UI preferences saved",
			"grouping_mode", prefs.GroupingMode,
			"expanded_groups_count", len(prefs.ExpandedGroups))
	}

	writeJSONOK(w, map[string]interface{}{
		"success": true,
	})
}

// loadUIPreferences loads UI preferences from the file.
func loadUIPreferences() (*UIPreferences, error) {
	path, err := appdir.UIPreferencesPath()
	if err != nil {
		return nil, err
	}

	var prefs UIPreferences
	if err := fileutil.ReadJSON(path, &prefs); err != nil {
		return nil, err
	}

	return &prefs, nil
}

// saveUIPreferences saves UI preferences to the file atomically.
func saveUIPreferences(prefs *UIPreferences) error {
	path, err := appdir.UIPreferencesPath()
	if err != nil {
		return err
	}

	return fileutil.WriteJSONAtomic(path, prefs, 0644)
}
