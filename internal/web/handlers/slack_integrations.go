package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/inercia/mitto/internal/session"
	"github.com/inercia/mitto/internal/slackbridge"
	"github.com/inercia/mitto/internal/slackcatalog"
	"github.com/inercia/mitto/internal/web/middleware"
)

const slackRequestBodyLimit = 64 << 10

type slackNameRequest struct {
	Name string `json:"name"`
}

type slackAppCreateRequest struct {
	Name     string `json:"name"`
	AppToken string `json:"app_token"`
}

type slackInstallationCreateRequest struct {
	Name     string `json:"name"`
	TeamID   string `json:"team_id,omitempty"`
	Token    string `json:"token"`
	BotToken string `json:"bot_token,omitempty"`
}

func (r slackInstallationCreateRequest) credential() (string, error) {
	token, legacy := strings.TrimSpace(r.Token), strings.TrimSpace(r.BotToken)
	if token != "" && legacy != "" && token != legacy {
		return "", fmt.Errorf("%w: conflicting installation credentials", slackcatalog.ErrInvalid)
	}
	if token != "" {
		return r.Token, nil
	}
	return r.BotToken, nil
}

type slackTokenRequest struct {
	Token string `json:"token"`
}

type slackOAuthClientRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type slackOAuthStartRequest struct {
	Name   string `json:"name,omitempty"`
	TeamID string `json:"team_id,omitempty"`
}

func (h *Handlers) slackService(w http.ResponseWriter) (*slackcatalog.Service, bool) {
	if h.deps.SlackCatalog == nil {
		writeRetryableUnavailable(w, "Slack integration catalog is unavailable", 5)
		return nil, false
	}
	return h.deps.SlackCatalog, true
}

func decodeSlackBody(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, slackRequestBodyLimit)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(value); err != nil {
		return writeSlackDecodeError(w, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return writeSlackDecodeError(w, err)
	}
	return true
}

func writeSlackDecodeError(w http.ResponseWriter, err error) bool {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeErrorJSON(w, http.StatusRequestEntityTooLarge, "", "Slack request body is too large")
		return false
	}
	writeErrorJSON(w, http.StatusBadRequest, "", "Invalid Slack request body")
	return false
}

// allowSlackCredentialWrite restricts token-bearing requests to Mitto's local
// listener. External listeners and reverse-proxied hosts do not provide a
// request-level TLS attestation that is safe to trust for bearer-token writes.
func allowSlackCredentialWrite(w http.ResponseWriter, r *http.Request) bool {
	if middleware.IsExternalConnection(r) || !middleware.IsLocalhostRequest(r) {
		writeErrorJSON(w, http.StatusForbidden, "", "Slack credentials can only be changed from the local Mitto interface")
		return false
	}
	return true
}

func writeSlackError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, slackcatalog.ErrRateLimited):
		w.Header().Set("Retry-After", strconv.Itoa(slackcatalog.RetryAfterSeconds(err)))
		writeErrorJSON(w, http.StatusTooManyRequests, "",
			"Slack is still rate limiting channel discovery after automatic retries")
	case errors.Is(err, slackcatalog.ErrInvalid):
		writeErrorJSON(w, http.StatusBadRequest, "", "Invalid Slack integration request")
	case errors.Is(err, slackcatalog.ErrNotFound):
		writeErrorJSON(w, http.StatusNotFound, "", "Slack integration not found")
	case errors.Is(err, slackcatalog.ErrReferenced):
		writeErrorJSON(w, http.StatusConflict, "", "Slack integration is referenced by an active loop")
	case errors.Is(err, slackcatalog.ErrOAuthRequired):
		writeErrorJSON(w, http.StatusConflict, "",
			"Slack did not return the app identity needed to safely bind this delegated-user credential. "+
				"Manual delegated-user setup is unavailable until Slack OAuth provenance is supported; use a bot token instead.")
	case errors.Is(err, slackcatalog.ErrConflict):
		writeErrorJSON(w, http.StatusConflict, "", "Slack integration conflicts with existing configuration")
	case errors.Is(err, slackcatalog.ErrUnavailable):
		writeRetryableUnavailable(w, "Slack integration is temporarily unavailable", 5)
	default:
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to update Slack integration")
	}
}

// HandleSlackConnections handles GET /api/slack/connections. It returns a
// credential-free point-in-time snapshot of every app's live Socket Mode
// connection status, so Settings > Slack has current delivery-health data on
// first load (the live feed only arrives on subsequent state changes via the
// slack_connection_status global-event broadcast).
func (h *Handlers) HandleSlackConnections(w http.ResponseWriter, _ *http.Request) {
	if h.deps.SlackManager == nil {
		writeJSONOK(w, map[string]any{"connections": []slackbridge.ConnectionStatus{}})
		return
	}
	writeJSONOK(w, map[string]any{"connections": h.deps.SlackManager.Status()})
}

func (h *Handlers) HandleSlackEnvironmentStatus(w http.ResponseWriter, _ *http.Request) {
	if h.deps.SlackEnvironment == nil {
		writeRetryableUnavailable(w, "Slack environment migration is unavailable", 5)
		return
	}
	status, err := h.deps.SlackEnvironment.Status()
	if err != nil {
		writeRetryableUnavailable(w, "Slack environment migration is temporarily unavailable", 5)
		return
	}
	writeJSONOK(w, status)
}

func (h *Handlers) HandleSlackEnvironmentImport(w http.ResponseWriter, r *http.Request) {
	if !allowSlackCredentialWrite(w, r) {
		return
	}
	if h.deps.SlackEnvironment == nil {
		writeRetryableUnavailable(w, "Slack environment migration is unavailable", 5)
		return
	}
	var request slackbridge.EnvironmentImportRequest
	if !decodeSlackBody(w, r, &request) {
		return
	}
	result, err := h.deps.SlackEnvironment.Import(r.Context(), request)
	if err != nil {
		writeSlackError(w, err)
		return
	}
	if h.deps.Store != nil && h.deps.BroadcastLoopUpdated != nil {
		sessionID := h.deps.SlackEnvironment.TargetSessionID()
		if loop, getErr := h.deps.Store.Loop(sessionID).Get(); getErr == nil {
			h.deps.BroadcastLoopUpdated(sessionID, loop)
		}
	}
	writeJSONOK(w, result)
}

func (h *Handlers) HandleSlackAppsList(w http.ResponseWriter, _ *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	apps, err := service.ListApps()
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, map[string]any{"apps": apps})
}

func (h *Handlers) HandleSlackAppCreate(w http.ResponseWriter, r *http.Request) {
	if !allowSlackCredentialWrite(w, r) {
		return
	}
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	var request slackAppCreateRequest
	if !decodeSlackBody(w, r, &request) {
		return
	}
	app, err := service.CreateApp(r.Context(), request.Name, request.AppToken)
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONCreated(w, app)
}

func (h *Handlers) HandleSlackAppGet(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	app, err := service.GetApp(r.PathValue("appId"))
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, app)
}

func (h *Handlers) HandleSlackAppPatch(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	var request slackNameRequest
	if !decodeSlackBody(w, r, &request) {
		return
	}
	app, err := service.RenameApp(r.PathValue("appId"), request.Name)
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, app)
}

func (h *Handlers) HandleSlackAppDelete(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	if err := service.DeleteApp(r.Context(), r.PathValue("appId")); err != nil {
		writeSlackError(w, err)
		return
	}
	writeNoContent(w)
}

func (h *Handlers) HandleSlackAppValidate(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	app, err := service.ValidateApp(r.Context(), r.PathValue("appId"))
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, app)
}

func (h *Handlers) HandleSlackAppToken(w http.ResponseWriter, r *http.Request) {
	if !allowSlackCredentialWrite(w, r) {
		return
	}
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	var request slackTokenRequest
	if !decodeSlackBody(w, r, &request) {
		return
	}
	app, err := service.ReplaceAppToken(r.Context(), r.PathValue("appId"), request.Token)
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, app)
}

func (h *Handlers) HandleSlackOAuthConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSONOK(w, h.slackOAuthConfig())
}

func (h *Handlers) HandleSlackOAuthClient(w http.ResponseWriter, r *http.Request) {
	if !allowSlackCredentialWrite(w, r) {
		return
	}
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	var request slackOAuthClientRequest
	if !decodeSlackBody(w, r, &request) {
		return
	}
	app, err := service.ConfigureOAuthClient(r.PathValue("appId"), request.ClientID, request.ClientSecret)
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, app)
}

func (h *Handlers) HandleSlackOAuthCreateStart(w http.ResponseWriter, r *http.Request) {
	h.handleSlackOAuthStart(w, r, r.PathValue("appId"), "")
}

func (h *Handlers) HandleSlackOAuthReplaceStart(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	installation, err := service.GetInstallation(r.PathValue("installationId"))
	if err != nil {
		writeSlackError(w, err)
		return
	}
	h.handleSlackOAuthStart(w, r, installation.AppID, installation.ID)
}

func (h *Handlers) handleSlackOAuthStart(w http.ResponseWriter, r *http.Request, appID, installationID string) {
	if !allowSlackCredentialWrite(w, r) {
		return
	}
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	config := h.slackOAuthConfig()
	if !config.Available {
		writeErrorJSON(w, http.StatusConflict, "", config.Message)
		return
	}
	var request slackOAuthStartRequest
	if !decodeSlackBody(w, r, &request) {
		return
	}
	start, err := service.StartOAuth(slackcatalog.OAuthStartRequest{AppID: appID, InstallationID: installationID,
		Name: request.Name, ExpectedTeamID: request.TeamID, RedirectURI: config.RedirectURI})
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, start)
}

func (h *Handlers) HandleSlackOAuthStatus(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	status, err := service.OAuthStatus(r.PathValue("flowId"))
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, status)
}

func (h *Handlers) HandleSlackOAuthCallback(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	status, err := service.CompleteOAuth(r.Context(), r.URL.Query().Get("state"), r.URL.Query().Get("code"), r.URL.Query().Get("error"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, slackcatalog.ErrUnavailable) {
			code = http.StatusServiceUnavailable
		}
		w.WriteHeader(code)
		message := status.Message
		if message == "" {
			message = "Slack authorization is invalid, expired, or already used. Return to Mitto and start again."
		}
		fmt.Fprintf(w, "<!doctype html><title>Slack authorization</title><main><h1>Authorization not completed</h1><p>%s</p></main>", html.EscapeString(message))
		return
	}
	fmt.Fprint(w, "<!doctype html><title>Slack authorization</title><main><h1>Slack authorization complete</h1><p>You can close this window and return to Mitto.</p></main>")
}

func (h *Handlers) slackOAuthConfig() slackcatalog.OAuthConfigView {
	const setupMessage = "Configure an HTTPS web.hooks.external_address before starting Slack OAuth."
	if h.deps.MittoConfig == nil {
		return slackcatalog.OAuthConfigView{Message: setupMessage}
	}
	base := strings.TrimSpace(h.deps.MittoConfig.Web.Hooks.ExternalAddress)
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return slackcatalog.OAuthConfigView{Message: setupMessage}
	}
	redirectURI := strings.TrimRight(base, "/") + h.deps.APIPrefix + "/api/slack/oauth/callback"
	return slackcatalog.OAuthConfigView{Available: true, RedirectURI: redirectURI}
}

func (h *Handlers) HandleSlackAppPrepareDelete(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	preview, err := service.PrepareDeleteApp(r.Context(), r.PathValue("appId"))
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, preview)
}

func (h *Handlers) HandleSlackAppReferencesDelete(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	result, err := service.RemoveAppReferences(r.Context(), r.PathValue("appId"))
	// Broadcast every reference that was actually mutated before checking err:
	// a partial failure still leaves earlier sessions changed on disk, and
	// connected clients (and this request's own Settings preview) must not
	// go stale just because a later session in the same batch failed.
	h.broadcastSlackReferenceRemovals(result.Removed)
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, result)
}

func (h *Handlers) HandleSlackInstallationsList(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	installations, err := service.ListInstallations(r.PathValue("appId"))
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, map[string]any{"installations": installations})
}

func (h *Handlers) HandleSlackInstallationCreate(w http.ResponseWriter, r *http.Request) {
	if !allowSlackCredentialWrite(w, r) {
		return
	}
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	var request slackInstallationCreateRequest
	if !decodeSlackBody(w, r, &request) {
		return
	}
	token, err := request.credential()
	if err != nil {
		writeSlackError(w, err)
		return
	}
	installation, err := service.CreateInstallation(r.Context(), r.PathValue("appId"), request.Name, request.TeamID, token)
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONCreated(w, installation)
}

func (h *Handlers) HandleSlackInstallationGet(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	installation, err := service.GetInstallation(r.PathValue("installationId"))
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, installation)
}

func (h *Handlers) HandleSlackInstallationPatch(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	var request slackNameRequest
	if !decodeSlackBody(w, r, &request) {
		return
	}
	installation, err := service.RenameInstallation(r.PathValue("installationId"), request.Name)
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, installation)
}

func (h *Handlers) HandleSlackInstallationDelete(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	if err := service.DeleteInstallation(r.Context(), r.PathValue("installationId")); err != nil {
		writeSlackError(w, err)
		return
	}
	writeNoContent(w)
}

func (h *Handlers) HandleSlackInstallationValidate(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	installation, err := service.ValidateInstallation(r.Context(), r.PathValue("installationId"))
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, installation)
}

func (h *Handlers) HandleSlackInstallationToken(w http.ResponseWriter, r *http.Request) {
	if !allowSlackCredentialWrite(w, r) {
		return
	}
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	var request slackTokenRequest
	if !decodeSlackBody(w, r, &request) {
		return
	}
	installation, err := service.ReplaceInstallationToken(r.Context(), r.PathValue("installationId"), request.Token)
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, installation)
}

func (h *Handlers) HandleSlackInstallationPrepareDelete(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	preview, err := service.PrepareDeleteInstallation(r.Context(), r.PathValue("installationId"))
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, preview)
}

func (h *Handlers) HandleSlackInstallationReferencesDelete(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	result, err := service.RemoveInstallationReferences(r.Context(), r.PathValue("installationId"))
	// See the matching comment in HandleSlackAppReferencesDelete: broadcast
	// before checking err so a partial failure still propagates every
	// mutation that actually landed on disk.
	h.broadcastSlackReferenceRemovals(result.Removed)
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, result)
}

func (h *Handlers) broadcastSlackReferenceRemovals(references []slackcatalog.Reference) {
	if h.deps.Store == nil || h.deps.BroadcastLoopUpdated == nil {
		return
	}
	for _, reference := range references {
		loop, err := h.deps.Store.Loop(reference.SessionID).Get()
		switch {
		case err == nil:
			h.deps.BroadcastLoopUpdated(reference.SessionID, loop)
		case errors.Is(err, session.ErrLoopNotFound):
			h.deps.BroadcastLoopUpdated(reference.SessionID, nil)
		default:
			if h.deps.Logger != nil {
				h.deps.Logger.Warn("slack: failed to read loop for reference-removal broadcast",
					"session_id", reference.SessionID, "error_class", "loop_read")
			}
		}
	}
}

func (h *Handlers) HandleSlackInstallationChannels(w http.ResponseWriter, r *http.Request) {
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "", "Invalid channel page limit")
			return
		}
		limit = parsed
	}
	page, err := service.Channels(r.Context(), r.PathValue("installationId"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeSlackError(w, err)
		return
	}
	writeJSONOK(w, page)
}
