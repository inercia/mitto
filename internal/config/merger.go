// Package config provides configuration management for Mitto.
package config

// ConfigItemSource indicates where a configuration item originated from.
// This is used to track provenance of merged configuration items.
type ConfigItemSource string

const (
	// SourceRCFile indicates the item was loaded from ~/.mittorc or equivalent.
	SourceRCFile ConfigItemSource = "rcfile"
	// SourceSettings indicates the item was defined in settings.json.
	SourceSettings ConfigItemSource = "settings"
	// SourceDefault indicates the item came from embedded defaults.
	SourceDefault ConfigItemSource = "default"
)

// MergeStrategy defines how items from different sources should be combined.
type MergeStrategy int

const (
	// MergeStrategyUnion combines all items from all sources, with higher priority sources
	// overriding lower priority ones when there are conflicts (by key/name).
	// Priority order: rcfile > settings > default
	MergeStrategyUnion MergeStrategy = iota

	// MergeStrategyReplace uses only items from the highest priority source that has any.
	// If rcfile has items, use only those. Otherwise use settings, etc.
	MergeStrategyReplace
)

// MergeResult contains the result of a merge operation along with metadata
// about which sources contributed to the final result.
type MergeResult[T any] struct {
	// Items is the merged list of items.
	Items []T
	// HasRCFileItems indicates whether any items came from the RC file.
	HasRCFileItems bool
	// HasSettingsItems indicates whether any items came from settings.json.
	HasSettingsItems bool
}

// Merger is a generic interface for merging configuration items from multiple sources.
// The type parameter T represents the item type being merged.
type Merger[T any] interface {
	// Merge combines items from rcfile and settings sources.
	// The rcfile items have higher priority than settings items.
	Merge(rcfileItems, settingsItems []T) MergeResult[T]
}

// KeyFunc extracts a unique key from an item for deduplication purposes.
type KeyFunc[T any] func(item T) string

// SourceSetter sets the source field on an item.
type SourceSetter[T any] func(item *T, source ConfigItemSource)

// SourceGetter gets the source field from an item.
type SourceGetter[T any] func(item T) ConfigItemSource

// GenericMerger provides a reusable implementation of the Merger interface.
// It uses key functions to identify duplicates and source setters to track provenance.
type GenericMerger[T any] struct {
	// KeyFunc extracts the unique key for deduplication.
	KeyFunc KeyFunc[T]
	// SetSource sets the source on an item (optional, can be nil).
	SetSource SourceSetter[T]
	// GetSource gets the source from an item (optional, can be nil).
	GetSource SourceGetter[T]
	// Strategy determines how items are combined.
	Strategy MergeStrategy
}

// Merge implements the Merger interface using a union strategy by default.
// RC file items take priority over settings items when keys conflict.
func (m *GenericMerger[T]) Merge(rcfileItems, settingsItems []T) MergeResult[T] {
	result := MergeResult[T]{
		HasRCFileItems:   len(rcfileItems) > 0,
		HasSettingsItems: len(settingsItems) > 0,
	}

	// Handle replace strategy
	if m.Strategy == MergeStrategyReplace {
		if len(rcfileItems) > 0 {
			result.Items = m.copyWithSource(rcfileItems, SourceRCFile)
			result.HasSettingsItems = false
			return result
		}
		result.Items = m.copyWithSource(settingsItems, SourceSettings)
		result.HasRCFileItems = false
		return result
	}

	// Union strategy: RC file items first (highest priority), then settings items
	seen := make(map[string]bool)
	result.Items = make([]T, 0, len(rcfileItems)+len(settingsItems))

	// Add RC file items first (highest priority)
	for _, item := range rcfileItems {
		key := m.KeyFunc(item)
		if key != "" && !seen[key] {
			seen[key] = true
			itemCopy := item
			if m.SetSource != nil {
				m.SetSource(&itemCopy, SourceRCFile)
			}
			result.Items = append(result.Items, itemCopy)
		}
	}

	// Add settings items (lower priority, skip duplicates)
	for _, item := range settingsItems {
		key := m.KeyFunc(item)
		if key != "" && !seen[key] {
			seen[key] = true
			itemCopy := item
			if m.SetSource != nil {
				m.SetSource(&itemCopy, SourceSettings)
			}
			result.Items = append(result.Items, itemCopy)
		}
	}

	return result
}

// copyWithSource creates a copy of items with the source field set.
func (m *GenericMerger[T]) copyWithSource(items []T, source ConfigItemSource) []T {
	result := make([]T, len(items))
	for i, item := range items {
		result[i] = item
		if m.SetSource != nil {
			m.SetSource(&result[i], source)
		}
	}
	return result
}

// FilterBySource returns only items matching the given source.
// Useful for extracting settings-only items before saving.
func FilterBySource[T any](items []T, getSource SourceGetter[T], source ConfigItemSource) []T {
	if getSource == nil {
		return items
	}
	var result []T
	for _, item := range items {
		if getSource(item) == source {
			result = append(result, item)
		}
	}
	return result
}

// FilterExcludeSource returns items that do NOT match the given source.
func FilterExcludeSource[T any](items []T, getSource SourceGetter[T], source ConfigItemSource) []T {
	if getSource == nil {
		return items
	}
	var result []T
	for _, item := range items {
		if getSource(item) != source {
			result = append(result, item)
		}
	}
	return result
}

// ============================================================================
// ACP Server Merger
// ============================================================================

// ACPServerMerger provides specialized merging for ACP server configurations.
var ACPServerMerger = &GenericMerger[ACPServer]{
	KeyFunc: func(srv ACPServer) string {
		return srv.Name
	},
	SetSource: func(srv *ACPServer, source ConfigItemSource) {
		srv.Source = source
	},
	GetSource: func(srv ACPServer) ConfigItemSource {
		return srv.Source
	},
	Strategy: MergeStrategyUnion,
}

// MergeACPServers combines ACP servers from RC file and settings.
// RC file servers take priority over settings servers with the same name.
// All servers are returned with their Source field set appropriately.
func MergeACPServers(rcfileServers, settingsServers []ACPServer) MergeResult[ACPServer] {
	return ACPServerMerger.Merge(rcfileServers, settingsServers)
}

// GetSettingsOnlyServers returns only servers that came from settings.json.
// Use this when saving to settings.json to avoid duplicating RC file servers.
func GetSettingsOnlyServers(servers []ACPServer) []ACPServer {
	return FilterBySource(servers, ACPServerMerger.GetSource, SourceSettings)
}

// GetRCFileServers returns only servers that came from the RC file.
func GetRCFileServers(servers []ACPServer) []ACPServer {
	return FilterBySource(servers, ACPServerMerger.GetSource, SourceRCFile)
}

// ACPServerSettingsMerger provides specialized merging for ACPServerSettings.
var ACPServerSettingsMerger = &GenericMerger[ACPServerSettings]{
	KeyFunc: func(srv ACPServerSettings) string {
		return srv.Name
	},
	SetSource: func(srv *ACPServerSettings, source ConfigItemSource) {
		srv.Source = source
	},
	GetSource: func(srv ACPServerSettings) ConfigItemSource {
		return srv.Source
	},
	Strategy: MergeStrategyUnion,
}

// MergeACPServerSettings combines ACPServerSettings from RC file and settings.
func MergeACPServerSettings(rcfileServers, settingsServers []ACPServerSettings) MergeResult[ACPServerSettings] {
	return ACPServerSettingsMerger.Merge(rcfileServers, settingsServers)
}

// GetSettingsOnlyServerSettings returns only ACPServerSettings from settings.json.
func GetSettingsOnlyServerSettings(servers []ACPServerSettings) []ACPServerSettings {
	return FilterBySource(servers, ACPServerSettingsMerger.GetSource, SourceSettings)
}
