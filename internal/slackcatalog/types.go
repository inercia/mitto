// Package slackcatalog manages process-global Slack app profiles and workspace
// installations. Secret values live exclusively in internal/secrets.
package slackcatalog

import (
	"errors"
	"time"
)

const (
	DocumentVersion             = 1
	AppTokenCredential          = "app-token"
	OAuthClientSecretCredential = "oauth-client-secret"
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
	ErrRateLimited = errors.New("slack rate limited")

	// ErrOAuthRequired is a distinct, value-free classification for a
	// delegated-user (xoxp) auth.test response that cannot prove its parent
	// Slack app identity (no bot_id and no app_id). It is deliberately
	// distinct from ErrConflict: the failure is not an identity mismatch
	// against existing state, it is the absence of any provable app
	// provenance to bind against in the first place. Manual delegated-user
	// create/replacement is fail-closed rejected until Slack OAuth code
	// exchange (which always returns app_id) is supported; see
	// docs/devel/slack-integration-catalog.md.
	ErrOAuthRequired = errors.New("slack delegated-user credential lacks provable app provenance")
)

type AppProfile struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	SlackAppID    string    `json:"slack_app_id"`
	OAuthClientID string    `json:"oauth_client_id,omitempty"`
	ValidatedAt   time.Time `json:"validated_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Installation struct {
	ID              string         `json:"id"`
	AppID           string         `json:"app_id"`
	Name            string         `json:"name"`
	CredentialKind  CredentialKind `json:"credential_kind"`
	TeamID          string         `json:"team_id"`
	TeamName        string         `json:"team_name,omitempty"`
	BotID           string         `json:"bot_id,omitempty"`
	BotUserID       string         `json:"bot_user_id,omitempty"`
	UserID          string         `json:"user_id,omitempty"`
	OAuthAuthorized bool           `json:"oauth_authorized,omitempty"`
	// GrantedUserScopes is the comma-separated delegated-user scope set minted
	// at the most recent successful OAuth authorization (see delegatedUserScopes
	// in oauth.go). Empty for installs predating this field, or created via
	// manual bot-token entry, which is treated as an unknown baseline (fail
	// open, never flagged as drifted). Scopes are not secret and are safe to
	// serialize.
	GrantedUserScopes string    `json:"granted_user_scopes,omitempty"`
	ValidatedAt       time.Time `json:"validated_at"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type AppView struct {
	AppProfile
	TokenConfigured             bool `json:"token_configured"`
	OAuthClientSecretConfigured bool `json:"oauth_client_secret_configured"`
}

type InstallationView struct {
	Installation
	TokenConfigured bool `json:"token_configured"`
	// NeedsReauthorization is derived (never persisted): true only when this is
	// a delegated-user installation whose granted scope baseline is missing a
	// currently-required scope AND the installation is referenced by an
	// enabled, unarchived onSlack subscription. See Service.decorateReauth.
	NeedsReauthorization bool `json:"needs_reauthorization"`
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

type OAuthIdentity struct {
	InstallationIdentity
	AccessToken string
}

type OAuthConfigView struct {
	Available   bool   `json:"available"`
	RedirectURI string `json:"redirect_uri,omitempty"`
	Message     string `json:"message,omitempty"`
}

type OAuthStartRequest struct {
	AppID          string
	InstallationID string
	Name           string
	ExpectedTeamID string
	RedirectURI    string
}

type OAuthStart struct {
	FlowID           string    `json:"flow_id"`
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type OAuthFlowStatus struct {
	FlowID         string    `json:"flow_id"`
	Status         string    `json:"status"`
	InstallationID string    `json:"installation_id,omitempty"`
	Error          string    `json:"error,omitempty"`
	Message        string    `json:"message,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
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

type ReferenceRemoval struct {
	Removed []Reference   `json:"removed"`
	Preview DeletePreview `json:"preview"`
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
