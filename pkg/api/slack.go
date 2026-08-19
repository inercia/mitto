package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// SlackApp is the non-secret Slack app profile returned by Mitto.
type SlackApp struct {
	ID                          string `json:"id"`
	Name                        string `json:"name"`
	SlackAppID                  string `json:"slack_app_id"`
	TokenConfigured             bool   `json:"token_configured"`
	OAuthClientID               string `json:"oauth_client_id,omitempty"`
	OAuthClientSecretConfigured bool   `json:"oauth_client_secret_configured"`
	ValidatedAt                 string `json:"validated_at"`
	CreatedAt                   string `json:"created_at"`
	UpdatedAt                   string `json:"updated_at"`
}

// SlackInstallation is the non-secret workspace installation returned by Mitto.
type SlackInstallation struct {
	ID              string `json:"id"`
	AppID           string `json:"app_id"`
	Name            string `json:"name"`
	CredentialKind  string `json:"credential_kind"`
	TeamID          string `json:"team_id"`
	TeamName        string `json:"team_name,omitempty"`
	BotID           string `json:"bot_id,omitempty"`
	BotUserID       string `json:"bot_user_id,omitempty"`
	UserID          string `json:"user_id,omitempty"`
	TokenConfigured bool   `json:"token_configured"`
	OAuthAuthorized bool   `json:"oauth_authorized,omitempty"`
	ValidatedAt     string `json:"validated_at"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type SlackChannel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsPrivate bool   `json:"is_private"`
	IsMember  bool   `json:"is_member"`
}

type SlackChannelPage struct {
	Channels   []SlackChannel `json:"channels"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

type SlackReference struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name,omitempty"`
}

type SlackDeletePreview struct {
	InstallationIDs []string         `json:"installation_ids"`
	References      []SlackReference `json:"references"`
}

type SlackReferenceRemoval struct {
	Removed []SlackReference   `json:"removed"`
	Preview SlackDeletePreview `json:"preview"`
}

// SlackEnvironmentStatus is the credential-free legacy import status.
type SlackEnvironmentStatus struct {
	Present          bool     `json:"present"`
	Complete         bool     `json:"complete"`
	MissingVariables []string `json:"missing_variables"`
	TeamID           string   `json:"team_id,omitempty"`
	ChannelID        string   `json:"channel_id,omitempty"`
	TargetSessionID  string   `json:"target_session_id,omitempty"`
	Active           bool     `json:"active"`
	Shadowed         bool     `json:"shadowed"`
}

type ImportSlackPoCRequest struct {
	AppID            string `json:"app_id,omitempty"`
	AppName          string `json:"app_name,omitempty"`
	InstallationID   string `json:"installation_id,omitempty"`
	InstallationName string `json:"installation_name,omitempty"`
}

type ImportSlackPoCResult struct {
	AppID               string `json:"app_id"`
	InstallationID      string `json:"installation_id"`
	AppCreated          bool   `json:"app_created"`
	InstallationCreated bool   `json:"installation_created"`
	SubscriptionCreated bool   `json:"subscription_created"`
	EnvironmentStopped  bool   `json:"environment_stopped"`
	ManagedActive       bool   `json:"managed_active"`
}

type CreateSlackAppRequest struct {
	Name     string `json:"name"`
	AppToken string `json:"app_token"`
}

type CreateSlackInstallationRequest struct {
	Name   string `json:"name"`
	TeamID string `json:"team_id,omitempty"`
	Token  string `json:"token,omitempty"`
	// BotToken is retained for compatibility with older Mitto servers.
	BotToken string `json:"bot_token,omitempty"`
}

type SlackOAuthConfig struct {
	Available   bool   `json:"available"`
	RedirectURI string `json:"redirect_uri,omitempty"`
	Message     string `json:"message,omitempty"`
}

type ConfigureSlackOAuthClientRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

type StartSlackOAuthRequest struct {
	Name   string `json:"name,omitempty"`
	TeamID string `json:"team_id,omitempty"`
}

type SlackOAuthStart struct {
	FlowID           string `json:"flow_id"`
	AuthorizationURL string `json:"authorization_url"`
	ExpiresAt        string `json:"expires_at"`
}

type SlackOAuthFlowStatus struct {
	FlowID         string `json:"flow_id"`
	Status         string `json:"status"`
	InstallationID string `json:"installation_id,omitempty"`
	Error          string `json:"error,omitempty"`
	Message        string `json:"message,omitempty"`
	ExpiresAt      string `json:"expires_at"`
}

func (c *Client) slackJSON(method, path string, body, result any, success ...int) error {
	var payload *bytes.Reader
	if body == nil {
		payload = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("slack API: marshal request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}
	contentType := ""
	if body != nil {
		contentType = "application/json"
	}
	request, err := c.newRequest(method, c.apiURL(path), contentType, payload)
	if err != nil {
		return fmt.Errorf("slack API: build request: %w", err)
	}
	response, err := c.do(request)
	if err != nil {
		return fmt.Errorf("slack API: %w", err)
	}
	defer response.Body.Close()
	ok := false
	for _, status := range success {
		if response.StatusCode == status {
			ok = true
			break
		}
	}
	if !ok {
		return c.apiError("Slack API", response)
	}
	if result == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(result); err != nil {
		return fmt.Errorf("slack API: decode response: %w", err)
	}
	return nil
}

func (c *Client) ListSlackApps() ([]SlackApp, error) {
	var response struct {
		Apps []SlackApp `json:"apps"`
	}
	err := c.slackJSON(http.MethodGet, "/api/slack/apps", nil, &response, http.StatusOK)
	return response.Apps, err
}

func (c *Client) GetSlackEnvironmentStatus() (*SlackEnvironmentStatus, error) {
	var status SlackEnvironmentStatus
	if err := c.slackJSON(http.MethodGet, "/api/slack/environment-import", nil, &status, http.StatusOK); err != nil {
		return nil, err
	}
	return &status, nil
}

func (c *Client) ImportSlackPoC(request ImportSlackPoCRequest) (*ImportSlackPoCResult, error) {
	var result ImportSlackPoCResult
	if err := c.slackJSON(http.MethodPost, "/api/slack/environment-import", request, &result, http.StatusOK); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) CreateSlackApp(request CreateSlackAppRequest) (*SlackApp, error) {
	var app SlackApp
	if err := c.slackJSON(http.MethodPost, "/api/slack/apps", request, &app, http.StatusCreated); err != nil {
		return nil, err
	}
	return &app, nil
}

func (c *Client) GetSlackApp(id string) (*SlackApp, error) {
	var app SlackApp
	if err := c.slackJSON(http.MethodGet, "/api/slack/apps/"+url.PathEscape(id), nil, &app, http.StatusOK); err != nil {
		return nil, err
	}
	return &app, nil
}

func (c *Client) RenameSlackApp(id, name string) (*SlackApp, error) {
	var app SlackApp
	if err := c.slackJSON(http.MethodPatch, "/api/slack/apps/"+url.PathEscape(id), map[string]string{"name": name}, &app, http.StatusOK); err != nil {
		return nil, err
	}
	return &app, nil
}

func (c *Client) ReplaceSlackAppToken(id, token string) (*SlackApp, error) {
	var app SlackApp
	path := "/api/slack/apps/" + url.PathEscape(id) + "/token"
	if err := c.slackJSON(http.MethodPut, path, map[string]string{"token": token}, &app, http.StatusOK); err != nil {
		return nil, err
	}
	return &app, nil
}

func (c *Client) GetSlackOAuthConfig() (*SlackOAuthConfig, error) {
	var config SlackOAuthConfig
	if err := c.slackJSON(http.MethodGet, "/api/slack/oauth/config", nil, &config, http.StatusOK); err != nil {
		return nil, err
	}
	return &config, nil
}

func (c *Client) ConfigureSlackOAuthClient(id string, request ConfigureSlackOAuthClientRequest) (*SlackApp, error) {
	var app SlackApp
	path := "/api/slack/apps/" + url.PathEscape(id) + "/oauth-client"
	if err := c.slackJSON(http.MethodPut, path, request, &app, http.StatusOK); err != nil {
		return nil, err
	}
	return &app, nil
}

func (c *Client) StartSlackOAuthInstallation(appID string, request StartSlackOAuthRequest) (*SlackOAuthStart, error) {
	var start SlackOAuthStart
	path := "/api/slack/apps/" + url.PathEscape(appID) + "/oauth/start"
	if err := c.slackJSON(http.MethodPost, path, request, &start, http.StatusOK); err != nil {
		return nil, err
	}
	return &start, nil
}

func (c *Client) StartSlackOAuthReplacement(installationID string) (*SlackOAuthStart, error) {
	var start SlackOAuthStart
	path := "/api/slack/installations/" + url.PathEscape(installationID) + "/oauth/start"
	if err := c.slackJSON(http.MethodPost, path, StartSlackOAuthRequest{}, &start, http.StatusOK); err != nil {
		return nil, err
	}
	return &start, nil
}

func (c *Client) GetSlackOAuthFlowStatus(flowID string) (*SlackOAuthFlowStatus, error) {
	var status SlackOAuthFlowStatus
	path := "/api/slack/oauth/flows/" + url.PathEscape(flowID)
	if err := c.slackJSON(http.MethodGet, path, nil, &status, http.StatusOK); err != nil {
		return nil, err
	}
	return &status, nil
}

func (c *Client) ValidateSlackApp(id string) (*SlackApp, error) {
	var app SlackApp
	path := "/api/slack/apps/" + url.PathEscape(id) + "/validate"
	if err := c.slackJSON(http.MethodPost, path, map[string]any{}, &app, http.StatusOK); err != nil {
		return nil, err
	}
	return &app, nil
}

func (c *Client) PrepareDeleteSlackApp(id string) (*SlackDeletePreview, error) {
	var preview SlackDeletePreview
	path := "/api/slack/apps/" + url.PathEscape(id) + "/prepare-delete"
	if err := c.slackJSON(http.MethodGet, path, nil, &preview, http.StatusOK); err != nil {
		return nil, err
	}
	return &preview, nil
}

func (c *Client) RemoveSlackAppReferences(id string) (*SlackReferenceRemoval, error) {
	var result SlackReferenceRemoval
	path := "/api/slack/apps/" + url.PathEscape(id) + "/references"
	if err := c.slackJSON(http.MethodDelete, path, nil, &result, http.StatusOK); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeleteSlackApp(id string) error {
	return c.slackJSON(http.MethodDelete, "/api/slack/apps/"+url.PathEscape(id), nil, nil, http.StatusNoContent)
}

func (c *Client) ListSlackInstallations(appID string) ([]SlackInstallation, error) {
	var response struct {
		Installations []SlackInstallation `json:"installations"`
	}
	path := "/api/slack/apps/" + url.PathEscape(appID) + "/installations"
	err := c.slackJSON(http.MethodGet, path, nil, &response, http.StatusOK)
	return response.Installations, err
}

func (c *Client) CreateSlackInstallation(appID string, request CreateSlackInstallationRequest) (*SlackInstallation, error) {
	var installation SlackInstallation
	path := "/api/slack/apps/" + url.PathEscape(appID) + "/installations"
	if err := c.slackJSON(http.MethodPost, path, request, &installation, http.StatusCreated); err != nil {
		return nil, err
	}
	return &installation, nil
}

func (c *Client) GetSlackInstallation(id string) (*SlackInstallation, error) {
	var installation SlackInstallation
	path := "/api/slack/installations/" + url.PathEscape(id)
	if err := c.slackJSON(http.MethodGet, path, nil, &installation, http.StatusOK); err != nil {
		return nil, err
	}
	return &installation, nil
}

func (c *Client) RenameSlackInstallation(id, name string) (*SlackInstallation, error) {
	var installation SlackInstallation
	path := "/api/slack/installations/" + url.PathEscape(id)
	if err := c.slackJSON(http.MethodPatch, path, map[string]string{"name": name}, &installation, http.StatusOK); err != nil {
		return nil, err
	}
	return &installation, nil
}

func (c *Client) ReplaceSlackInstallationToken(id, token string) (*SlackInstallation, error) {
	var installation SlackInstallation
	path := "/api/slack/installations/" + url.PathEscape(id) + "/token"
	if err := c.slackJSON(http.MethodPut, path, map[string]string{"token": token}, &installation, http.StatusOK); err != nil {
		return nil, err
	}
	return &installation, nil
}

func (c *Client) ValidateSlackInstallation(id string) (*SlackInstallation, error) {
	var installation SlackInstallation
	path := "/api/slack/installations/" + url.PathEscape(id) + "/validate"
	if err := c.slackJSON(http.MethodPost, path, map[string]any{}, &installation, http.StatusOK); err != nil {
		return nil, err
	}
	return &installation, nil
}

func (c *Client) PrepareDeleteSlackInstallation(id string) (*SlackDeletePreview, error) {
	var preview SlackDeletePreview
	path := "/api/slack/installations/" + url.PathEscape(id) + "/prepare-delete"
	if err := c.slackJSON(http.MethodGet, path, nil, &preview, http.StatusOK); err != nil {
		return nil, err
	}
	return &preview, nil
}

func (c *Client) RemoveSlackInstallationReferences(id string) (*SlackReferenceRemoval, error) {
	var result SlackReferenceRemoval
	path := "/api/slack/installations/" + url.PathEscape(id) + "/references"
	if err := c.slackJSON(http.MethodDelete, path, nil, &result, http.StatusOK); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeleteSlackInstallation(id string) error {
	return c.slackJSON(http.MethodDelete, "/api/slack/installations/"+url.PathEscape(id), nil, nil, http.StatusNoContent)
}

func (c *Client) ListSlackChannels(installationID, cursor string, limit int) (*SlackChannelPage, error) {
	query := url.Values{}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if limit != 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	path := "/api/slack/installations/" + url.PathEscape(installationID) + "/channels"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var page SlackChannelPage
	if err := c.slackJSON(http.MethodGet, path, nil, &page, http.StatusOK); err != nil {
		return nil, err
	}
	return &page, nil
}
