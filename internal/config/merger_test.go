package config

import (
	"testing"
)

func TestGenericMerger_UnionStrategy(t *testing.T) {
	merger := &GenericMerger[ACPServer]{
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

	tests := []struct {
		name          string
		rcfileItems   []ACPServer
		settingsItems []ACPServer
		wantCount     int
		wantRCFile    bool
		wantSettings  bool
		wantNames     []string
		wantSources   []ConfigItemSource
	}{
		{
			name: "both sources with overlap",
			rcfileItems: []ACPServer{
				{Name: "auggie", Command: "auggie --acp"},
				{Name: "claude", Command: "claude --acp"},
			},
			settingsItems: []ACPServer{
				{Name: "claude", Command: "claude --different"}, // duplicate - should be skipped
				{Name: "custom", Command: "custom-agent --acp"},
			},
			wantCount:    3,
			wantRCFile:   true,
			wantSettings: true,
			wantNames:    []string{"auggie", "claude", "custom"},
			wantSources:  []ConfigItemSource{SourceRCFile, SourceRCFile, SourceSettings},
		},
		{
			name: "only rcfile items",
			rcfileItems: []ACPServer{
				{Name: "auggie", Command: "auggie --acp"},
			},
			settingsItems: nil,
			wantCount:     1,
			wantRCFile:    true,
			wantSettings:  false,
			wantNames:     []string{"auggie"},
			wantSources:   []ConfigItemSource{SourceRCFile},
		},
		{
			name:        "only settings items",
			rcfileItems: nil,
			settingsItems: []ACPServer{
				{Name: "custom", Command: "custom-agent --acp"},
			},
			wantCount:    1,
			wantRCFile:   false,
			wantSettings: true,
			wantNames:    []string{"custom"},
			wantSources:  []ConfigItemSource{SourceSettings},
		},
		{
			name:          "empty both",
			rcfileItems:   nil,
			settingsItems: nil,
			wantCount:     0,
			wantRCFile:    false,
			wantSettings:  false,
			wantNames:     nil,
			wantSources:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := merger.Merge(tt.rcfileItems, tt.settingsItems)

			if len(result.Items) != tt.wantCount {
				t.Errorf("got %d items, want %d", len(result.Items), tt.wantCount)
			}

			if result.HasRCFileItems != tt.wantRCFile {
				t.Errorf("HasRCFileItems = %v, want %v", result.HasRCFileItems, tt.wantRCFile)
			}

			if result.HasSettingsItems != tt.wantSettings {
				t.Errorf("HasSettingsItems = %v, want %v", result.HasSettingsItems, tt.wantSettings)
			}

			for i, name := range tt.wantNames {
				if i >= len(result.Items) {
					t.Errorf("missing item at index %d", i)
					continue
				}
				if result.Items[i].Name != name {
					t.Errorf("item[%d].Name = %q, want %q", i, result.Items[i].Name, name)
				}
			}

			for i, source := range tt.wantSources {
				if i >= len(result.Items) {
					continue
				}
				if result.Items[i].Source != source {
					t.Errorf("item[%d].Source = %q, want %q", i, result.Items[i].Source, source)
				}
			}
		})
	}
}

func TestGenericMerger_ReplaceStrategy(t *testing.T) {
	merger := &GenericMerger[ACPServer]{
		KeyFunc: func(srv ACPServer) string {
			return srv.Name
		},
		SetSource: func(srv *ACPServer, source ConfigItemSource) {
			srv.Source = source
		},
		Strategy: MergeStrategyReplace,
	}

	tests := []struct {
		name          string
		rcfileItems   []ACPServer
		settingsItems []ACPServer
		wantCount     int
		wantRCFile    bool
		wantSettings  bool
	}{
		{
			name: "both sources - rcfile wins",
			rcfileItems: []ACPServer{
				{Name: "auggie", Command: "auggie --acp"},
			},
			settingsItems: []ACPServer{
				{Name: "custom", Command: "custom --acp"},
			},
			wantCount:    1, // Only rcfile items
			wantRCFile:   true,
			wantSettings: false,
		},
		{
			name:        "only settings - settings used",
			rcfileItems: nil,
			settingsItems: []ACPServer{
				{Name: "custom", Command: "custom --acp"},
			},
			wantCount:    1,
			wantRCFile:   false,
			wantSettings: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := merger.Merge(tt.rcfileItems, tt.settingsItems)

			if len(result.Items) != tt.wantCount {
				t.Errorf("got %d items, want %d", len(result.Items), tt.wantCount)
			}
		})
	}
}

func TestMergeACPServers(t *testing.T) {
	rcServers := []ACPServer{
		{Name: "auggie", Command: "auggie --acp"},
		{Name: "claude", Command: "claude-code --acp"},
	}
	settingsServers := []ACPServer{
		{Name: "claude", Command: "claude-different"}, // Will be overridden by RC
		{Name: "custom", Command: "custom-agent"},
	}

	result := MergeACPServers(rcServers, settingsServers)

	if len(result.Items) != 3 {
		t.Fatalf("expected 3 servers, got %d", len(result.Items))
	}

	// Check order and sources
	expected := []struct {
		name   string
		source ConfigItemSource
	}{
		{"auggie", SourceRCFile},
		{"claude", SourceRCFile}, // RC file takes priority
		{"custom", SourceSettings},
	}

	for i, e := range expected {
		if result.Items[i].Name != e.name {
			t.Errorf("item[%d].Name = %q, want %q", i, result.Items[i].Name, e.name)
		}
		if result.Items[i].Source != e.source {
			t.Errorf("item[%d].Source = %q, want %q", i, result.Items[i].Source, e.source)
		}
	}

	// Check that claude has the RC file command, not settings
	for _, srv := range result.Items {
		if srv.Name == "claude" && srv.Command != "claude-code --acp" {
			t.Errorf("claude should have RC file command, got %q", srv.Command)
		}
	}
}

func TestGetSettingsOnlyServers(t *testing.T) {
	servers := []ACPServer{
		{Name: "auggie", Source: SourceRCFile},
		{Name: "claude", Source: SourceRCFile},
		{Name: "custom", Source: SourceSettings},
	}

	settings := GetSettingsOnlyServers(servers)

	if len(settings) != 1 {
		t.Fatalf("expected 1 settings server, got %d", len(settings))
	}

	if settings[0].Name != "custom" {
		t.Errorf("expected custom server, got %s", settings[0].Name)
	}
}

func TestGetRCFileServers(t *testing.T) {
	servers := []ACPServer{
		{Name: "auggie", Source: SourceRCFile},
		{Name: "claude", Source: SourceRCFile},
		{Name: "custom", Source: SourceSettings},
	}

	rcServers := GetRCFileServers(servers)

	if len(rcServers) != 2 {
		t.Fatalf("expected 2 RC file servers, got %d", len(rcServers))
	}

	names := make(map[string]bool)
	for _, srv := range rcServers {
		names[srv.Name] = true
	}

	if !names["auggie"] || !names["claude"] {
		t.Errorf("expected auggie and claude, got %v", names)
	}
}

func TestFilterBySource(t *testing.T) {
	items := []ACPServer{
		{Name: "a", Source: SourceRCFile},
		{Name: "b", Source: SourceSettings},
		{Name: "c", Source: SourceDefault},
	}

	getSource := func(srv ACPServer) ConfigItemSource { return srv.Source }

	rcOnly := FilterBySource(items, getSource, SourceRCFile)
	if len(rcOnly) != 1 || rcOnly[0].Name != "a" {
		t.Errorf("FilterBySource(SourceRCFile) failed")
	}

	settingsOnly := FilterBySource(items, getSource, SourceSettings)
	if len(settingsOnly) != 1 || settingsOnly[0].Name != "b" {
		t.Errorf("FilterBySource(SourceSettings) failed")
	}
}

func TestFilterExcludeSource(t *testing.T) {
	items := []ACPServer{
		{Name: "a", Source: SourceRCFile},
		{Name: "b", Source: SourceSettings},
		{Name: "c", Source: SourceRCFile},
	}

	getSource := func(srv ACPServer) ConfigItemSource { return srv.Source }

	nonRC := FilterExcludeSource(items, getSource, SourceRCFile)
	if len(nonRC) != 1 || nonRC[0].Name != "b" {
		t.Errorf("FilterExcludeSource(SourceRCFile) failed, got %v", nonRC)
	}
}
