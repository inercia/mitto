package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/inercia/mitto/internal/slackcatalog"
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
	BotToken string `json:"bot_token"`
}

type slackTokenRequest struct {
	Token string `json:"token"`
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
	return parseJSONBody(w, r, value)
}

func writeSlackError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, slackcatalog.ErrInvalid):
		writeErrorJSON(w, http.StatusBadRequest, "", err.Error())
	case errors.Is(err, slackcatalog.ErrNotFound):
		writeErrorJSON(w, http.StatusNotFound, "", "Slack integration not found")
	case errors.Is(err, slackcatalog.ErrConflict), errors.Is(err, slackcatalog.ErrReferenced):
		writeErrorJSON(w, http.StatusConflict, "", err.Error())
	case errors.Is(err, slackcatalog.ErrUnavailable):
		writeRetryableUnavailable(w, "Slack integration is temporarily unavailable", 5)
	default:
		writeErrorJSON(w, http.StatusInternalServerError, "", "Failed to update Slack integration")
	}
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
	service, ok := h.slackService(w)
	if !ok {
		return
	}
	var request slackInstallationCreateRequest
	if !decodeSlackBody(w, r, &request) {
		return
	}
	installation, err := service.CreateInstallation(r.Context(), r.PathValue("appId"), request.Name, request.TeamID, request.BotToken)
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
