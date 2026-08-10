package client

import (
	"reflect"
	"testing"
)

// conversationCentricRouteMethods pins the mitto-rwxq.7 Plan comment's audit:
// every conversation-centric REST route this SDK claims to cover (per the
// "REST Surface Coverage" section in doc.go) must have a corresponding
// exported *Client method. This is deliberately a local, cheap coverage
// check (route -> method name), not a live parse of internal/web/routes.go's
// route table -- pkg/api must not import internal/ packages. A future
// server-side addition in this area that has no SDK method here will not be
// caught automatically; this only guards regressions in the set audited by
// the plan (removing/renaming a method without updating this map).
var conversationCentricRouteMethods = map[string]string{
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
}

// TestRouteCoverage_ConversationCentricSurface asserts every route claimed
// in conversationCentricRouteMethods above has a matching exported method on
// *Client, per the Test phase's coverage requirement.
func TestRouteCoverage_ConversationCentricSurface(t *testing.T) {
	clientType := reflect.TypeOf(&Client{})
	for route, methodName := range conversationCentricRouteMethods {
		t.Run(route, func(t *testing.T) {
			if _, ok := clientType.MethodByName(methodName); !ok {
				t.Errorf("route %q claims SDK method %q, but *Client has no such method", route, methodName)
			}
		})
	}
}

// TestRouteCoverage_NoDuplicateMethodNames guards against copy-paste errors
// in the map above (two routes accidentally pointing at the same method name
// would silently under-count real coverage).
func TestRouteCoverage_NoDuplicateMethodNames(t *testing.T) {
	seen := make(map[string]string, len(conversationCentricRouteMethods))
	for route, methodName := range conversationCentricRouteMethods {
		if other, ok := seen[methodName]; ok {
			t.Errorf("method %q is claimed by both %q and %q", methodName, other, route)
		}
		seen[methodName] = route
	}
}
