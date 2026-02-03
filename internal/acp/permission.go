// Package acp provides ACP (Agent Communication Protocol) client implementation.
package acp

import (
	"github.com/coder/acp-go-sdk"
)

// AutoApprovePermission automatically selects the best option for permission auto-approval.
// It prefers "allow" options (AllowOnce or AllowAlways) if available,
// otherwise falls back to the first option.
// If no options are available, it returns a cancelled response.
//
// This function is used by all ACP client implementations (CLI, Web, Auxiliary)
// to provide consistent auto-approval behavior.
func AutoApprovePermission(options []acp.PermissionOption) acp.RequestPermissionResponse {
	// Prefer an allow option if present
	for _, opt := range options {
		if opt.Kind == acp.PermissionOptionKindAllowOnce || opt.Kind == acp.PermissionOptionKindAllowAlways {
			return acp.RequestPermissionResponse{
				Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{OptionId: opt.OptionId},
				},
			}
		}
	}

	// Otherwise choose the first option
	if len(options) > 0 {
		return acp.RequestPermissionResponse{
			Outcome: acp.RequestPermissionOutcome{
				Selected: &acp.RequestPermissionOutcomeSelected{OptionId: options[0].OptionId},
			},
		}
	}

	// No options available, cancel
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
	}
}

// CancelledPermissionResponse returns a cancelled permission response.
func CancelledPermissionResponse() acp.RequestPermissionResponse {
	return acp.RequestPermissionResponse{
		Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}},
	}
}
