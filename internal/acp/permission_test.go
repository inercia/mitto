package acp

import (
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestAutoApprovePermission_PreferAllowOnce(t *testing.T) {
	options := []acp.PermissionOption{
		{OptionId: "deny", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce},
		{OptionId: "allow-once", Name: "Allow Once", Kind: acp.PermissionOptionKindAllowOnce},
		{OptionId: "allow-always", Name: "Allow Always", Kind: acp.PermissionOptionKindAllowAlways},
	}

	resp := AutoApprovePermission(options)

	if resp.Outcome.Selected == nil {
		t.Fatal("expected Selected outcome")
	}
	if resp.Outcome.Selected.OptionId != "allow-once" {
		t.Errorf("OptionId = %q, want %q", resp.Outcome.Selected.OptionId, "allow-once")
	}
}

func TestAutoApprovePermission_PreferAllowAlways(t *testing.T) {
	options := []acp.PermissionOption{
		{OptionId: "deny", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce},
		{OptionId: "allow-always", Name: "Allow Always", Kind: acp.PermissionOptionKindAllowAlways},
	}

	resp := AutoApprovePermission(options)

	if resp.Outcome.Selected == nil {
		t.Fatal("expected Selected outcome")
	}
	// Should select allow-always since it's the first allow option
	if resp.Outcome.Selected.OptionId != "allow-always" {
		t.Errorf("OptionId = %q, want %q", resp.Outcome.Selected.OptionId, "allow-always")
	}
}

func TestAutoApprovePermission_FallbackToFirst(t *testing.T) {
	options := []acp.PermissionOption{
		{OptionId: "first", Name: "First", Kind: acp.PermissionOptionKindRejectOnce},
		{OptionId: "second", Name: "Second", Kind: acp.PermissionOptionKindRejectOnce},
	}

	resp := AutoApprovePermission(options)

	if resp.Outcome.Selected == nil {
		t.Fatal("expected Selected outcome")
	}
	if resp.Outcome.Selected.OptionId != "first" {
		t.Errorf("OptionId = %q, want %q", resp.Outcome.Selected.OptionId, "first")
	}
}

func TestAutoApprovePermission_NoOptions(t *testing.T) {
	resp := AutoApprovePermission(nil)

	if resp.Outcome.Cancelled == nil {
		t.Error("expected Cancelled outcome when no options")
	}
	if resp.Outcome.Selected != nil {
		t.Error("Selected should be nil when no options")
	}
}

func TestAutoApprovePermission_EmptyOptions(t *testing.T) {
	resp := AutoApprovePermission([]acp.PermissionOption{})

	if resp.Outcome.Cancelled == nil {
		t.Error("expected Cancelled outcome when empty options")
	}
}

func TestSelectPermissionOption_ValidIndex(t *testing.T) {
	options := []acp.PermissionOption{
		{OptionId: "first", Name: "First"},
		{OptionId: "second", Name: "Second"},
		{OptionId: "third", Name: "Third"},
	}

	tests := []struct {
		index  int
		wantID string
	}{
		{0, "first"},
		{1, "second"},
		{2, "third"},
	}

	for _, tt := range tests {
		resp := SelectPermissionOption(options, tt.index)
		if resp.Outcome.Selected == nil {
			t.Errorf("index %d: expected Selected outcome", tt.index)
			continue
		}
		if string(resp.Outcome.Selected.OptionId) != tt.wantID {
			t.Errorf("index %d: OptionId = %q, want %q", tt.index, resp.Outcome.Selected.OptionId, tt.wantID)
		}
	}
}

func TestSelectPermissionOption_InvalidIndex(t *testing.T) {
	options := []acp.PermissionOption{
		{OptionId: "first", Name: "First"},
		{OptionId: "second", Name: "Second"},
	}

	tests := []int{-1, 2, 3, 100}

	for _, index := range tests {
		resp := SelectPermissionOption(options, index)
		if resp.Outcome.Cancelled == nil {
			t.Errorf("index %d: expected Cancelled outcome for invalid index", index)
		}
		if resp.Outcome.Selected != nil {
			t.Errorf("index %d: Selected should be nil for invalid index", index)
		}
	}
}

func TestSelectPermissionOption_EmptyOptions(t *testing.T) {
	resp := SelectPermissionOption(nil, 0)

	if resp.Outcome.Cancelled == nil {
		t.Error("expected Cancelled outcome for nil options")
	}
}

func TestCancelledPermissionResponse(t *testing.T) {
	resp := CancelledPermissionResponse()

	if resp.Outcome.Cancelled == nil {
		t.Error("expected Cancelled outcome")
	}
	if resp.Outcome.Selected != nil {
		t.Error("Selected should be nil")
	}
}
