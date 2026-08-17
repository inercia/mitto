// Package slackcatalog manages process-global Slack app profiles and workspace
// installations. Secret values live exclusively in internal/secrets.
package slackcatalog

import (
	"errors"
	"time"
)

const (
	DocumentVersion        = 1
	AppTokenCredential     = "app-token"
	BotTokenCredential     = "bot-token"
	DefaultChannelPageSize = 100
	MaxChannelPageSize     = 200
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
	ID          string    `json:"id"`
	AppID       string    `json:"app_id"`
	Name        string    `json:"name"`
	TeamID      string    `json:"team_id"`
	TeamName    string    `json:"team_name,omitempty"`
	BotID       string    `json:"bot_id"`
	BotUserID   string    `json:"bot_user_id"`
	ValidatedAt time.Time `json:"validated_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
	SlackAppID string
	TeamID     string
	TeamName   string
	BotID      string
	BotUserID  string
}

type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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

type document struct {
	Version       int            `json:"version"`
	Apps          []AppProfile   `json:"apps"`
	Installations []Installation `json:"installations"`
}
