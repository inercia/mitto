package api

// RouteCoverage pins the mitto-rwxq.7 Plan comment's audit: every
// conversation-centric REST route this SDK claims to cover (per the "REST
// Surface Coverage" section in doc.go) must have a corresponding exported
// *Client method. This is deliberately a local, cheap coverage map (route ->
// method name), not a live parse of internal/web/routes.go's route table --
// pkg/api must not import internal/ packages.
//
// Exported (mitto-7gta.24) so internal/web's cross-package route-coverage
// gate can read the same data without duplicating it: that gate derives this
// SDK's covered-path set from RouteCoverage's keys, treating every other
// declared server route as either covered by the JS SDK or listed in an
// explicit exemption. Non-conversation routes (workspaces, prompts, issues,
// global settings, agents, dashboard) are intentionally absent here by
// design (doc.go's "REST Surface Coverage" section) and are expected to be
// exempted on the Go side of that gate, not added to this map.
var RouteCoverage = map[string]string{
	"GET /api/sessions":              "ListSessions",
	"POST /api/sessions":             "CreateSession",
	"GET /api/sessions/running":      "ListRunningSessions",
	"GET /api/sessions/{id}":         "GetSession",
	"PATCH /api/sessions/{id}":       "UpdateSession",
	"DELETE /api/sessions/{id}":      "DeleteSession",
	"GET /api/sessions/{id}/events":  "GetSessionEvents",
	"GET /api/sessions/{id}/changes": "GetSessionChanges",

	"GET /api/sessions/{id}/settings":   "GetSessionSettings",
	"PATCH /api/sessions/{id}/settings": "UpdateSessionSettings",
	"POST /api/sessions/{id}/flush":     "FlushSession",
	"GET /api/sessions/{id}/user-data":  "GetSessionUserData",
	"PUT /api/sessions/{id}/user-data":  "SetSessionUserData",
	"POST /api/sessions/{id}/prune":     "PruneSession",

	"GET /api/sessions/{id}/images":              "ListImages",
	"POST /api/sessions/{id}/images":             "UploadImage",
	"GET /api/sessions/{id}/images/{imageId}":    "GetImage",
	"DELETE /api/sessions/{id}/images/{imageId}": "DeleteImage",
	"GET /api/sessions/{id}/files":               "ListFiles",
	"POST /api/sessions/{id}/files":              "UploadFile",
	"GET /api/sessions/{id}/files/{fileId}":      "GetFile",
	"DELETE /api/sessions/{id}/files/{fileId}":   "DeleteFile",

	"GET /api/sessions/{id}/queue":               "ListQueue",
	"POST /api/sessions/{id}/queue":              "AddToQueue",
	"GET /api/sessions/{id}/queue/{msgId}":       "GetQueueMessage",
	"DELETE /api/sessions/{id}/queue/{msgId}":    "RemoveFromQueue",
	"DELETE /api/sessions/{id}/queue":            "ClearQueue",
	"POST /api/sessions/{id}/queue/{msgId}/move": "MoveQueueMessage",
	"GET /api/sessions/{id}/prompt-arg-cache":    "GetPromptArgCache",

	"PUT /api/sessions/{id}/loop":                             "SetLoop",
	"GET /api/sessions/{id}/loop":                             "GetLoop",
	"PATCH /api/sessions/{id}/loop":                           "PatchLoop",
	"DELETE /api/sessions/{id}/loop":                          "DeleteLoop",
	"POST /api/sessions/{id}/loop/restore":                    "RestoreLoop",
	"POST /api/sessions/{id}/loop/acknowledge-stopped-reason": "AcknowledgeLoopStoppedReason",
	"GET /api/sessions/{id}/loop/suggest-from-recent":         "SuggestLoopFromRecent",
	"POST /api/sessions/{id}/loop/run-now":                    "RunLoopNow",

	// mitto-pscc.9/.10: `mitto auth status`/`rotate` are required (by the
	// internal/cmd no-raw-net/http gate, mitto-pscc.10) to talk to the
	// server through this SDK rather than net/http directly, even though
	// these three endpoints sit outside the conversation-centric scope
	// described above.
	"GET /api/health":             "GetHealth",
	"GET /api/auth-info":          "GetAuthInfo",
	"POST /api/auth/rotate-token": "RotateSharedToken",
}
