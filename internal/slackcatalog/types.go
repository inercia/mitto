// Package slackcatalog manages process-global Slack app profiles and workspace
// installations. Secret values live exclusively in internal/secrets.
package slackcatalog

import (
	"errors"
	"time"
)

const (
	DocumentVersion    = 1
	AppTokenCredential = "app-token"
	// InstallationTokenCredential retains the original vault key so existing
	// bot credentials remain available without reading or rewriting secrets.
	InstallationTokenCredential = "bot-token"
	BotTokenCredential          = InstallationTokenCredential
	DefaultChannelPageSize      = 100
	MaxChannelPageSize          = 200
)

type CredentialKind string

const (
	CredentialKindBot  CredentialKind = "bot"
	CredentialKindUser CredentialKind = "user"
)

var (
	ErrInvalid     = errors.New("invalid Slack catalog input")
	ErrNotFound    = errors.New("slack catalog resource not found")
	ErrConflict    = errors.New("slack catalog conflict")
	ErrReferenced  = errors.New("slack catalog resource is referenced")
	ErrUnavailable = errors.New("slack integration unavailable")
)

type AppProfile struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	SlackAppID  string    `json:"slack_app_id"`
	ValidatedAt time.Time `json:"validated_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Installation struct {
	ID             string         `json:"id"`
	AppID          string         `json:"app_id"`
	Name           string         `json:"name"`
	CredentialKind CredentialKind `json:"credential_kind"`
	TeamID         string         `json:"team_id"`
	TeamName       string         `json:"team_name,omitempty"`
	BotID          string         `json:"bot_id,omitempty"`
	BotUserID      string         `json:"bot_user_id,omitempty"`
	UserID         string         `json:"user_id,omitempty"`
	ValidatedAt    time.Time      `json:"validated_at"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

type AppView struct {
	AppProfile
	TokenConfigured bool `json:"token_configured"`
}

type InstallationView struct {
	Installation
	TokenConfigured bool `json:"token_configured"`
}

type InstallationIdentity struct {
	CredentialKind CredentialKind
	SlackAppID     string
	TeamID         string
	TeamName       string
	BotID          string
	BotUserID      string
	UserID         string
}

type Channel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsPrivate bool   `json:"is_private"`
	IsMember  bool   `json:"is_member"`
}

type ChannelPage struct {
	Channels   []Channel `json:"channels"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type Reference struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name,omitempty"`
}

type DeletePreview struct {
	InstallationIDs []string    `json:"installation_ids"`
	References      []Reference `json:"references"`
}

// Change is a value-free post-commit catalog notification.
type Change struct {
	AppID          string
	InstallationID string
	Credential     bool
}

// PoCImportRequest carries the legacy environment values into the catalog.
// Token fields are backend-only and must never be serialized or logged.
type PoCImportRequest struct {
	AppID            string
	AppName          string
	InstallationID   string
	InstallationName string
	ExpectedTeamID   string
	AppToken         string
	BotToken         string
}

// PoCImportResult contains only stable, non-secret migration metadata.
type PoCImportResult struct {
	AppID               string `json:"app_id"`
	InstallationID      string `json:"installation_id"`
	AppCreated          bool   `json:"app_created"`
	InstallationCreated bool   `json:"installation_created"`
}

type document struct {
	Version       int            `json:"version"`
	Apps          []AppProfile   `json:"apps"`
	Installations []Installation `json:"installations"`
}
