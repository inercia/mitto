package slackcatalog

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/inercia/mitto/internal/secrets"
)

const (
	defaultOAuthFlowTTL = 10 * time.Minute
	delegatedUserScopes = "channels:read,channels:history,groups:read,groups:history"
)

type oauthFlow struct {
	FlowID, AppID, InstallationID, Name, ExpectedTeamID, RedirectURI, ClientID string
	ExpiresAt                                                                  time.Time
}

// scopesDrifted reports whether the currently-required comma-separated scope
// list contains a scope missing from the granted comma-separated baseline.
// An empty granted baseline (pre-migration installs, or installs created via
// manual token entry rather than OAuth) is an unknown quantity, not a known
// drift, so it fails open and returns false. Pure and deterministic: no I/O,
// no token values, safe to unit test directly.
func scopesDrifted(granted, required string) bool {
	if strings.TrimSpace(granted) == "" {
		return false
	}
	have := make(map[string]bool)
	for _, scope := range strings.Split(granted, ",") {
		if scope = strings.TrimSpace(scope); scope != "" {
			have[scope] = true
		}
	}
	for _, scope := range strings.Split(required, ",") {
		if scope = strings.TrimSpace(scope); scope != "" && !have[scope] {
			return true
		}
	}
	return false
}

func (s *Service) ConfigureOAuthClient(appID, clientID, clientSecret string) (AppView, error) {
	if err := validateResourceID(appID); err != nil {
		return AppView{}, err
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || len(clientID) > 256 || strings.ContainsAny(clientID, " \t\r\n") {
		return AppView{}, fmt.Errorf("%w: malformed OAuth client ID", ErrInvalid)
	}
	clientSecret = strings.TrimSpace(clientSecret)
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.store.Load()
	if err != nil {
		return AppView{}, err
	}
	idx := appIndex(doc, appID)
	if idx < 0 {
		return AppView{}, ErrNotFound
	}
	doc.Apps[idx].OAuthClientID = clientID
	doc.Apps[idx].UpdatedAt = s.now().UTC()
	if clientSecret != "" {
		if err := s.commitCredentialAndDocument(secrets.SlackAppCredential(appID, OAuthClientSecretCredential), clientSecret, doc); err != nil {
			return AppView{}, err
		}
	} else if err := s.store.Save(doc); err != nil {
		return AppView{}, err
	}
	return s.appView(doc.Apps[idx])
}

func (s *Service) StartOAuth(request OAuthStartRequest) (OAuthStart, error) {
	if err := validateResourceID(request.AppID); err != nil {
		return OAuthStart{}, err
	}
	if request.InstallationID == "" {
		name, err := validateName(request.Name)
		if err != nil {
			return OAuthStart{}, err
		}
		request.Name = name
	} else if err := validateResourceID(request.InstallationID); err != nil {
		return OAuthStart{}, err
	}
	if request.ExpectedTeamID != "" && !teamIDPattern.MatchString(request.ExpectedTeamID) {
		return OAuthStart{}, fmt.Errorf("%w: malformed expected team ID", ErrInvalid)
	}
	redirect, err := url.Parse(request.RedirectURI)
	if err != nil || redirect.Scheme != "https" || redirect.Host == "" || redirect.Fragment != "" {
		return OAuthStart{}, fmt.Errorf("%w: OAuth redirect URI must be HTTPS", ErrInvalid)
	}
	s.mu.Lock()
	doc, err := s.store.Load()
	if err != nil {
		s.mu.Unlock()
		return OAuthStart{}, err
	}
	appIdx := appIndex(doc, request.AppID)
	if appIdx < 0 {
		s.mu.Unlock()
		return OAuthStart{}, ErrNotFound
	}
	app := doc.Apps[appIdx]
	if request.InstallationID != "" {
		idx := installationIndex(doc, request.InstallationID)
		if idx < 0 || doc.Installations[idx].AppID != request.AppID {
			s.mu.Unlock()
			return OAuthStart{}, ErrNotFound
		}
		request.ExpectedTeamID = doc.Installations[idx].TeamID
	}
	s.mu.Unlock()
	status, err := s.credentials.Status(secrets.SlackAppCredential(request.AppID, OAuthClientSecretCredential))
	if err != nil || app.OAuthClientID == "" || !status.Configured {
		return OAuthStart{}, fmt.Errorf("%w: Slack OAuth client is not configured", ErrUnavailable)
	}
	flowID, err := randomOAuthToken()
	if err != nil {
		return OAuthStart{}, err
	}
	state, err := randomOAuthToken()
	if err != nil {
		return OAuthStart{}, err
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.oauthTTL)
	flow := oauthFlow{FlowID: flowID, AppID: request.AppID, InstallationID: request.InstallationID,
		Name: request.Name, ExpectedTeamID: request.ExpectedTeamID, RedirectURI: request.RedirectURI,
		ClientID: app.OAuthClientID, ExpiresAt: expiresAt}
	s.oauthMu.Lock()
	s.pruneOAuthLocked(now)
	s.oauthStates[state] = flow
	s.oauthResults[flowID] = OAuthFlowStatus{FlowID: flowID, Status: "pending", ExpiresAt: expiresAt}
	s.oauthMu.Unlock()
	query := url.Values{"client_id": {app.OAuthClientID}, "redirect_uri": {request.RedirectURI},
		"state": {state}, "user_scope": {delegatedUserScopes}}
	return OAuthStart{FlowID: flowID, AuthorizationURL: "https://slack.com/oauth/v2/authorize?" + query.Encode(), ExpiresAt: expiresAt}, nil
}

func (s *Service) CompleteOAuth(ctx context.Context, state, code, providerError string) (OAuthFlowStatus, error) {
	flow, err := s.consumeOAuthFlow(strings.TrimSpace(state))
	if err != nil {
		return OAuthFlowStatus{}, err
	}
	if providerError != "" {
		status := s.failOAuth(flow, "authorization_cancelled", "Slack authorization was cancelled or denied.")
		return status, fmt.Errorf("%w: Slack authorization declined", ErrInvalid)
	}
	if strings.TrimSpace(code) == "" {
		status := s.failOAuth(flow, "missing_code", "Slack did not return an authorization code.")
		return status, fmt.Errorf("%w: OAuth code missing", ErrInvalid)
	}
	provider, ok := s.slack.(SlackOAuthProvider)
	if !ok {
		status := s.failOAuth(flow, "unavailable", "Slack OAuth is unavailable.")
		return status, ErrUnavailable
	}
	secret, err := s.credentials.Resolve(secrets.SlackAppCredential(flow.AppID, OAuthClientSecretCredential))
	if err != nil {
		status := s.failOAuth(flow, "unavailable", "The Slack OAuth client secret is unavailable.")
		return status, fmt.Errorf("%w: OAuth client secret unavailable", ErrUnavailable)
	}
	identity, err := provider.ExchangeOAuth(ctx, flow.ClientID, secret, code, flow.RedirectURI)
	if err != nil {
		status := s.failOAuth(flow, oauthErrorCode(err), "Slack authorization could not be completed.")
		return status, err
	}
	identity.InstallationIdentity = normalizeInstallationIdentity(identity.InstallationIdentity)
	if err := validateInstallationIdentity(identity.InstallationIdentity); err != nil {
		status := s.failOAuth(flow, "identity_mismatch", "Slack returned invalid installation identity.")
		return status, err
	}
	installation, err := s.commitOAuthInstallation(flow, identity)
	if err != nil {
		status := s.failOAuth(flow, oauthErrorCode(err), "Slack authorization did not match the selected app or workspace.")
		return status, err
	}
	status := OAuthFlowStatus{FlowID: flow.FlowID, Status: "succeeded", InstallationID: installation.ID, ExpiresAt: flow.ExpiresAt}
	s.oauthMu.Lock()
	s.oauthResults[flow.FlowID] = status
	s.oauthMu.Unlock()
	return status, nil
}

func (s *Service) OAuthStatus(flowID string) (OAuthFlowStatus, error) {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" || len(flowID) > 128 {
		return OAuthFlowStatus{}, ErrInvalid
	}
	now := s.now().UTC()
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()
	status, ok := s.oauthResults[flowID]
	if !ok {
		return OAuthFlowStatus{}, ErrNotFound
	}
	if status.Status == "pending" && !now.Before(status.ExpiresAt) {
		status.Status, status.Error, status.Message = "failed", "expired", "Slack authorization expired; start again."
		s.oauthResults[flowID] = status
	}
	return status, nil
}

func (s *Service) consumeOAuthFlow(state string) (oauthFlow, error) {
	if state == "" || len(state) > 128 {
		return oauthFlow{}, ErrInvalid
	}
	now := s.now().UTC()
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()
	flow, ok := s.oauthStates[state]
	if !ok {
		return oauthFlow{}, fmt.Errorf("%w: OAuth state is invalid or already used", ErrInvalid)
	}
	delete(s.oauthStates, state)
	if !now.Before(flow.ExpiresAt) {
		status := s.oauthResults[flow.FlowID]
		status.Status, status.Error, status.Message = "failed", "expired", "Slack authorization expired; start again."
		s.oauthResults[flow.FlowID] = status
		return oauthFlow{}, fmt.Errorf("%w: OAuth state expired", ErrInvalid)
	}
	return flow, nil
}

func (s *Service) commitOAuthInstallation(flow oauthFlow, identity OAuthIdentity) (InstallationView, error) {
	if identity.CredentialKind != CredentialKindUser {
		return InstallationView{}, fmt.Errorf("%w: OAuth did not return a delegated-user credential", ErrConflict)
	}
	s.mu.Lock()
	view, observer, change, err := func() (InstallationView, func(Change), Change, error) {
		doc, err := s.store.Load()
		if err != nil {
			return InstallationView{}, nil, Change{}, err
		}
		appIdx := appIndex(doc, flow.AppID)
		if appIdx < 0 {
			return InstallationView{}, nil, Change{}, ErrNotFound
		}
		if doc.Apps[appIdx].SlackAppID != identity.SlackAppID || doc.Apps[appIdx].OAuthClientID != flow.ClientID {
			return InstallationView{}, nil, Change{}, fmt.Errorf("%w: OAuth app identity does not match", ErrConflict)
		}
		if flow.ExpectedTeamID != "" && flow.ExpectedTeamID != identity.TeamID {
			return InstallationView{}, nil, Change{}, fmt.Errorf("%w: OAuth team identity does not match", ErrConflict)
		}
		now := s.now().UTC()
		var idx int
		if flow.InstallationID != "" {
			idx = installationIndex(doc, flow.InstallationID)
			if idx < 0 || doc.Installations[idx].AppID != flow.AppID || doc.Installations[idx].TeamID != identity.TeamID {
				return InstallationView{}, nil, Change{}, fmt.Errorf("%w: replacement installation identity does not match", ErrConflict)
			}
		} else {
			for i := range doc.Installations {
				if doc.Installations[i].AppID == flow.AppID && doc.Installations[i].TeamID == identity.TeamID {
					return InstallationView{}, nil, Change{}, fmt.Errorf("%w: workspace already installed for this app", ErrConflict)
				}
			}
			doc.Installations = append(doc.Installations, Installation{ID: uuid.NewString(), AppID: flow.AppID,
				Name: flow.Name, CreatedAt: now})
			idx = len(doc.Installations) - 1
		}
		installation := &doc.Installations[idx]
		applyInstallationIdentity(installation, identity.InstallationIdentity)
		installation.OAuthAuthorized = true
		// Capture the scope baseline granted by this authorization. Re-running
		// this same flow (re-authorization) refreshes the baseline to the
		// current constant, which self-clears any previously-detected drift.
		installation.GrantedUserScopes = delegatedUserScopes
		installation.ValidatedAt, installation.UpdatedAt = now, now
		ref := secrets.SlackInstallationCredential(installation.ID, InstallationTokenCredential)
		if err := s.commitCredentialAndDocument(ref, identity.AccessToken, doc); err != nil {
			return InstallationView{}, nil, Change{}, err
		}
		s.invalidateChannelsLocked(installation.ID)
		change := Change{AppID: flow.AppID, InstallationID: installation.ID, Credential: true}
		return InstallationView{Installation: *installation, TokenConfigured: true}, s.observer, change, nil
	}()
	s.mu.Unlock()
	if err != nil {
		return InstallationView{}, err
	}
	if observer != nil {
		observer(change)
	}
	return view, nil
}

func (s *Service) failOAuth(flow oauthFlow, code, message string) OAuthFlowStatus {
	status := OAuthFlowStatus{FlowID: flow.FlowID, Status: "failed", Error: code, Message: message, ExpiresAt: flow.ExpiresAt}
	s.oauthMu.Lock()
	s.oauthResults[flow.FlowID] = status
	s.oauthMu.Unlock()
	return status
}

func (s *Service) pruneOAuthLocked(now time.Time) {
	for state, flow := range s.oauthStates {
		if !now.Before(flow.ExpiresAt) {
			delete(s.oauthStates, state)
		}
	}
	for id, status := range s.oauthResults {
		if now.After(status.ExpiresAt.Add(s.oauthTTL)) {
			delete(s.oauthResults, id)
		}
	}
}

func randomOAuthToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("%w: generate OAuth state", ErrUnavailable)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func oauthErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrConflict):
		return "identity_mismatch"
	case errors.Is(err, ErrInvalid):
		return "authorization_rejected"
	default:
		return "unavailable"
	}
}
