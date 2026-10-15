package conversation

import acp "github.com/coder/acp-go-sdk"

// ChildSessionUpdate is an OpenCode child lifecycle event received over the
// shared ACP connection. The ACP process layer validates the wire envelope
// before constructing this value.
type ChildSessionUpdate struct {
	RootSessionID   acp.SessionId
	ChildSessionID  acp.SessionId
	ParentSessionID acp.SessionId
	Depth           int
	Title           *string
	Type            string
	Status          string
	Error           *ChildSessionUpdateError
	Update          *acp.SessionUpdate
}

// ChildSessionUpdateError contains OpenCode's non-sensitive lifecycle error
// classification and message. Callers must not include it in logs.
type ChildSessionUpdateError struct {
	Type    string
	Message string
}
