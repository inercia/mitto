package conversation

import (
	"fmt"
	"strings"

	"github.com/inercia/mitto/internal/acp"
	"github.com/inercia/mitto/internal/processors"
)

const processorAuthNoticeTitle = "🔐 AI agent authentication required"

// FormatProcessorFailureNotice replaces processor-authentication errors with
// one safe, actionable workspace notice. fallback is returned unchanged for
// other failures so each dispatch path can retain its existing wording.
func FormatProcessorFailureNotice(
	workspaceUUID, workspaceName string,
	lastErr error,
	fallback UINotifyRequest,
) (UINotifyRequest, bool) {
	if !acp.IsAuthError(lastErr) {
		return fallback, true
	}
	if !processors.ClaimAuthDispatchNotice(workspaceUUID) {
		return UINotifyRequest{}, false
	}

	workspaceName = strings.TrimSpace(workspaceName)
	if workspaceName == "" {
		workspaceName = strings.TrimSpace(workspaceUUID)
	}
	if workspaceName == "" {
		workspaceName = "this workspace"
	}

	return UINotifyRequest{
		Title: processorAuthNoticeTitle,
		Message: fmt.Sprintf(
			"A processor could not be dispatched because the AI agent's authentication has expired for workspace %q. Re-authenticate the CLI in a terminal (for example, `claude auth login` for Claude Code or `auggie auth login` for Auggie), then retry the pending work.",
			workspaceName,
		),
		Style: "warning",
	}, true
}
