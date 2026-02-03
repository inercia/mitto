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

func TestCancelledPermissionResponse(t *testing.T) {
	resp := CancelledPermissionResponse()

	if resp.Outcome.Cancelled == nil {
		t.Error("expected Cancelled outcome")
	}
	if resp.Outcome.Selected != nil {
		t.Error("Selected should be nil")
	}
}
