package agents

import "testing"

// TestAgentDefinition_StableID_Precedence pins the documented precedence
// chain AgentID > ACPId > Name > DirName (mitto-lrt.9), and confirms that
// DisplayName never influences the result (it may be freely renamed).
func TestAgentDefinition_StableID_Precedence(t *testing.T) {
	tests := []struct {
		name string
		def  AgentDefinition
		want string
	}{
		{
			name: "AgentID wins over everything else",
			def: AgentDefinition{
				DirName: "dir-name",
				Metadata: AgentMetadata{
					AgentID:     "explicit-agent-id",
					ACPId:       "acp-id",
					Name:        "some-name",
					DisplayName: "Some Display Name",
				},
			},
			want: "explicit-agent-id",
		},
		{
			name: "ACPId wins when AgentID absent",
			def: AgentDefinition{
				DirName: "dir-name",
				Metadata: AgentMetadata{
					ACPId:       "acp-id",
					Name:        "some-name",
					DisplayName: "Some Display Name",
				},
			},
			want: "acp-id",
		},
		{
			name: "Name wins when AgentID and ACPId absent",
			def: AgentDefinition{
				DirName: "dir-name",
				Metadata: AgentMetadata{
					Name:        "some-name",
					DisplayName: "Some Display Name",
				},
			},
			want: "some-name",
		},
		{
			name: "DirName is the last-resort fallback",
			def: AgentDefinition{
				DirName:  "dir-name",
				Metadata: AgentMetadata{DisplayName: "Some Display Name"},
			},
			want: "dir-name",
		},
		{
			name: "empty metadata and DirName yields empty string",
			def:  AgentDefinition{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := tt.def
			if got := def.StableID(); got != tt.want {
				t.Fatalf("StableID() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAgentDefinition_StableID_RenameInvariant confirms that renaming
// DisplayName alone (the common "rename an agent" operation) never changes
// StableID, whichever precedence tier is in effect.
func TestAgentDefinition_StableID_RenameInvariant(t *testing.T) {
	def := AgentDefinition{
		DirName: "dir-name",
		Metadata: AgentMetadata{
			ACPId:       "acp-id",
			DisplayName: "Original Name",
		},
	}
	before := def.StableID()

	def.Metadata.DisplayName = "Renamed Agent"
	after := def.StableID()

	if before != after {
		t.Fatalf("StableID() changed after DisplayName rename: before=%q after=%q", before, after)
	}
	if before != "acp-id" {
		t.Fatalf("StableID() = %q, want %q", before, "acp-id")
	}
}

// TestAgentDefinition_Reach confirms every on-disk AgentDefinition reports
// ReachLocal, regardless of its identity fields (mitto-lrt.9: a remote-only
// provider is represented as a ConfiguredProvider with no matching StableID
// instead, never as an AgentDefinition).
func TestAgentDefinition_Reach(t *testing.T) {
	def := AgentDefinition{DirName: "dir-name"}
	if got := def.Reach(); got != ReachLocal {
		t.Fatalf("Reach() = %q, want %q", got, ReachLocal)
	}
}
