package slackcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	appTokenPattern          = regexp.MustCompile(`^xapp-[0-9]+-(A[A-Z0-9]+)-`)
	installationTokenPattern = regexp.MustCompile(`^xox[a-z]-`)
)

const (
	slackRequestTimeout = 30 * time.Second
	slackRetryAttempts  = 4
	slackRetryBudget    = 2 * time.Minute
	slackRetryBaseDelay = 500 * time.Millisecond
	slackRetryMaxDelay  = 8 * time.Second
)

type SlackProvider interface {
	ValidateApp(context.Context, string) (string, error)
	ValidateInstallation(context.Context, string) (InstallationIdentity, error)
	ListChannels(context.Context, string, string, int) (ChannelPage, error)
}

type SlackOAuthProvider interface {
	ExchangeOAuth(context.Context, string, string, string, string) (OAuthIdentity, error)
	RevalidateOAuthInstallation(context.Context, string, string) (InstallationIdentity, error)
}

type SlackClient struct {
	APIURL string
	Client *http.Client

	sleepFn     func(context.Context, time.Duration) error
	randFn      func() float64
	nowFn       func() time.Time
	maxAttempts int
	retryBudget time.Duration
}

type slackChannelListResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
	Channels []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		IsArchived bool   `json:"is_archived"`
		IsPrivate  bool   `json:"is_private"`
		IsMember   bool   `json:"is_member"`
	} `json:"channels"`
	Metadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

func NewSlackClient() *SlackClient {
	return &SlackClient{Client: &http.Client{Timeout: slackRequestTimeout}}
}

func (c *SlackClient) endpoint(method string) string {
	base := c.APIURL
	if base == "" {
		base = "https://slack.com/api/"
	}
	return strings.TrimRight(base, "/") + "/" + method
}

func (c *SlackClient) ValidateApp(ctx context.Context, token string) (string, error) {
	token = strings.TrimSpace(token)
	matches := appTokenPattern.FindStringSubmatch(token)
	if len(matches) != 2 {
		return "", fmt.Errorf("%w: malformed app token", ErrInvalid)
	}
	var response struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		URL   string `json:"url"`
	}
	if err := c.call(ctx, token, "apps.connections.open", nil, &response); err != nil {
		return "", err
	}
	if !response.OK || response.URL == "" {
		return "", slackAPIError("apps.connections.open", response.Error)
	}
	return matches[1], nil
}

func (c *SlackClient) ValidateInstallation(ctx context.Context, token string) (InstallationIdentity, error) {
	return c.validateInstallation(ctx, token, "")
}

func (c *SlackClient) RevalidateOAuthInstallation(ctx context.Context, token, slackAppID string) (InstallationIdentity, error) {
	if !slackAppIDPattern.MatchString(slackAppID) {
		return InstallationIdentity{}, fmt.Errorf("%w: malformed OAuth app identity", ErrInvalid)
	}
	return c.validateInstallation(ctx, token, slackAppID)
}

func (c *SlackClient) validateInstallation(ctx context.Context, token, oauthAppID string) (InstallationIdentity, error) {
	token = strings.TrimSpace(token)
	if !installationTokenPattern.MatchString(token) {
		return InstallationIdentity{}, fmt.Errorf("%w: malformed installation credential", ErrInvalid)
	}
	var auth struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		AppID  string `json:"app_id"`
		TeamID string `json:"team_id"`
		Team   string `json:"team"`
		BotID  string `json:"bot_id"`
		UserID string `json:"user_id"`
	}
	if err := c.call(ctx, token, "auth.test", nil, &auth); err != nil {
		return InstallationIdentity{}, err
	}
	if !auth.OK {
		return InstallationIdentity{}, slackAPIError("auth.test", auth.Error)
	}
	if auth.TeamID == "" || auth.UserID == "" {
		return InstallationIdentity{}, fmt.Errorf("%w: auth.test omitted required identity fields", ErrConflict)
	}
	if auth.BotID == "" {
		if auth.AppID == "" {
			if oauthAppID != "" {
				return InstallationIdentity{CredentialKind: CredentialKindUser, SlackAppID: oauthAppID,
					TeamID: auth.TeamID, TeamName: auth.Team, UserID: auth.UserID}, nil
			}
			// A standard user-token auth.test response omits app_id (only
			// oauth.v2.access and bot tokens carry it). Without app_id there
			// is no provable binding to a specific Slack app/team, so this
			// is classified distinctly from a generic identity conflict:
			// OAuth-required, not "retry with different credentials".
			return InstallationIdentity{}, fmt.Errorf("%w: auth.test omitted delegated-user app identity", ErrOAuthRequired)
		}
		return InstallationIdentity{CredentialKind: CredentialKindUser, SlackAppID: auth.AppID,
			TeamID: auth.TeamID, TeamName: auth.Team, UserID: auth.UserID}, nil
	}
	var bot struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Bot   struct {
			AppID string `json:"app_id"`
		} `json:"bot"`
	}
	if err := c.call(ctx, token, "bots.info", url.Values{"bot": {auth.BotID}, "team_id": {auth.TeamID}}, &bot); err != nil {
		return InstallationIdentity{}, err
	}
	if !bot.OK {
		return InstallationIdentity{}, slackAPIError("bots.info", bot.Error)
	}
	if bot.Bot.AppID == "" {
		return InstallationIdentity{}, fmt.Errorf("%w: bots.info omitted app identity", ErrConflict)
	}
	return InstallationIdentity{CredentialKind: CredentialKindBot, SlackAppID: bot.Bot.AppID,
		TeamID: auth.TeamID, TeamName: auth.Team, BotID: auth.BotID, BotUserID: auth.UserID}, nil
}

func (c *SlackClient) ExchangeOAuth(ctx context.Context, clientID, clientSecret, code, redirectURI string) (OAuthIdentity, error) {
	values := url.Values{
		"client_id":     {strings.TrimSpace(clientID)},
		"client_secret": {strings.TrimSpace(clientSecret)},
		"code":          {strings.TrimSpace(code)},
		"redirect_uri":  {strings.TrimSpace(redirectURI)},
	}
	for _, key := range []string{"client_id", "client_secret", "code", "redirect_uri"} {
		if values.Get(key) == "" {
			return OAuthIdentity{}, fmt.Errorf("%w: incomplete OAuth exchange", ErrInvalid)
		}
	}
	var response struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		AppID string `json:"app_id"`
		Team  struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
		AuthedUser struct {
			ID          string `json:"id"`
			AccessToken string `json:"access_token"`
		} `json:"authed_user"`
	}
	if err := c.callForm(ctx, "oauth.v2.access", values, &response); err != nil {
		return OAuthIdentity{}, err
	}
	if !response.OK {
		return OAuthIdentity{}, slackAPIError("oauth.v2.access", response.Error)
	}
	identity := InstallationIdentity{CredentialKind: CredentialKindUser, SlackAppID: response.AppID,
		TeamID: response.Team.ID, TeamName: response.Team.Name, UserID: response.AuthedUser.ID}
	if response.AuthedUser.AccessToken == "" {
		return OAuthIdentity{}, fmt.Errorf("%w: oauth.v2.access omitted delegated-user token", ErrConflict)
	}
	return OAuthIdentity{InstallationIdentity: identity, AccessToken: response.AuthedUser.AccessToken}, nil
}

func (c *SlackClient) ListChannels(ctx context.Context, token, cursor string, limit int) (ChannelPage, error) {
	values := url.Values{
		"exclude_archived": {"true"},
		"limit":            {strconv.Itoa(limit)},
		"types":            {"public_channel,private_channel"},
	}
	if cursor != "" {
		values.Set("cursor", cursor)
	}
	var response slackChannelListResponse
	if err := c.withRetry(ctx, func(attemptCtx context.Context) error {
		response = slackChannelListResponse{}
		if err := c.callOnce(attemptCtx, token, "conversations.list", values, &response); err != nil {
			return err
		}
		if !response.OK {
			return slackAPIError("conversations.list", response.Error)
		}
		return nil
	}); err != nil {
		return ChannelPage{}, err
	}
	page := ChannelPage{Channels: make([]Channel, 0, len(response.Channels)), NextCursor: response.Metadata.NextCursor}
	for _, ch := range response.Channels {
		if ch.ID != "" && ch.Name != "" && !ch.IsArchived {
			page.Channels = append(page.Channels, Channel{
				ID: ch.ID, Name: ch.Name, IsPrivate: ch.IsPrivate, IsMember: ch.IsMember,
			})
		}
	}
	return page, nil
}

func (c *SlackClient) call(ctx context.Context, token, method string, values url.Values, result any) error {
	return c.callOnce(ctx, token, method, values, result)
}

func (c *SlackClient) callOnce(ctx context.Context, token, method string, values url.Values, result any) error {
	if values == nil {
		values = url.Values{}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(method), strings.NewReader(values.Encode()))
	if err != nil {
		return fmt.Errorf("%w: build Slack request", ErrUnavailable)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &slackRetryError{cause: ErrUnavailable, method: method}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		delay, hasDelay := parseRetryAfter(response.Header.Get("Retry-After"), c.now())
		response.Body.Close()
		if response.StatusCode == http.StatusTooManyRequests {
			return &slackRetryError{cause: ErrRateLimited, method: method, status: response.StatusCode,
				retryAfter: delay, hasRetryAfter: hasDelay}
		}
		if isRetryableSlackStatus(response.StatusCode) {
			return &slackRetryError{cause: ErrUnavailable, method: method, status: response.StatusCode,
				retryAfter: delay, hasRetryAfter: hasDelay}
		}
		if response.StatusCode >= 400 && response.StatusCode < 500 {
			return fmt.Errorf("%w: %s returned HTTP %d", ErrInvalid, method, response.StatusCode)
		}
		return fmt.Errorf("%w: %s returned HTTP %d", ErrUnavailable, method, response.StatusCode)
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(result); err != nil {
		return fmt.Errorf("%w: decode %s response", ErrUnavailable, method)
	}
	return nil
}

type slackRetryError struct {
	cause         error
	method        string
	status        int
	retryAfter    time.Duration
	hasRetryAfter bool
}

func (e *slackRetryError) Error() string {
	if e.status != 0 {
		return fmt.Sprintf("%s returned HTTP %d", e.method, e.status)
	}
	if errors.Is(e.cause, ErrRateLimited) {
		return e.method + " was rate limited"
	}
	return e.method + " request failed"
}

func (e *slackRetryError) Unwrap() error { return e.cause }

func (c *SlackClient) withRetry(ctx context.Context, attemptFn func(context.Context) error) error {
	attempts := c.maxAttempts
	if attempts <= 0 {
		attempts = slackRetryAttempts
	}
	budget := c.retryBudget
	if budget <= 0 {
		budget = slackRetryBudget
	}
	budgetCtx := ctx
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > budget {
		var cancel context.CancelFunc
		budgetCtx, cancel = context.WithTimeout(ctx, budget)
		defer cancel()
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		err := attemptFn(budgetCtx)
		if err == nil {
			return nil
		}
		var retryErr *slackRetryError
		if !errors.As(err, &retryErr) || attempt == attempts {
			return err
		}
		delay := c.retryDelay(retryErr, attempt)
		if deadline, ok := budgetCtx.Deadline(); ok && delay > time.Until(deadline) {
			return err
		}
		if err := c.sleep(budgetCtx, delay); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return retryErr
		}
	}
	return fmt.Errorf("%w: Slack retry attempts exhausted", ErrUnavailable)
}

func (c *SlackClient) retryDelay(retryErr *slackRetryError, attempt int) time.Duration {
	if retryErr.hasRetryAfter {
		return retryErr.retryAfter
	}
	delay := slackRetryBaseDelay << (attempt - 1)
	if delay > slackRetryMaxDelay {
		delay = slackRetryMaxDelay
	}
	return time.Duration(float64(delay) * (0.5 + c.random()))
}

func (c *SlackClient) sleep(ctx context.Context, delay time.Duration) error {
	if c.sleepFn != nil {
		return c.sleepFn(ctx, delay)
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *SlackClient) random() float64 {
	if c.randFn != nil {
		return c.randFn()
	}
	return rand.Float64()
}

func (c *SlackClient) now() time.Time {
	if c.nowFn != nil {
		return c.nowFn()
	}
	return time.Now()
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	return max(when.Sub(now), 0), true
}

func isRetryableSlackStatus(status int) bool {
	switch status {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// RetryAfterSeconds returns a safe retry hint for a classified Slack error.
func RetryAfterSeconds(err error) int {
	var retryErr *slackRetryError
	if !errors.As(err, &retryErr) || !retryErr.hasRetryAfter || retryErr.retryAfter <= 0 {
		return 1
	}
	return int((retryErr.retryAfter + time.Second - 1) / time.Second)
}

func (c *SlackClient) callForm(ctx context.Context, method string, values url.Values, result any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(method), strings.NewReader(values.Encode()))
	if err != nil {
		return fmt.Errorf("%w: build Slack request", ErrUnavailable)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %s request failed", ErrUnavailable, method)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%w: %s returned HTTP %d", ErrUnavailable, method, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(result); err != nil {
		return fmt.Errorf("%w: decode %s response", ErrUnavailable, method)
	}
	return nil
}

func slackAPIError(method, code string) error {
	if code == "" {
		code = "unknown_error"
	}
	switch code {
	case "invalid_auth", "not_authed", "token_revoked", "token_expired", "account_inactive",
		"invalid_app_token", "missing_scope", "not_allowed_token_type":
		return fmt.Errorf("%w: %s failed (%s)", ErrInvalid, method, code)
	case "ratelimited":
		return &slackRetryError{cause: ErrRateLimited, method: method}
	case "internal_error", "fatal_error", "service_unavailable", "request_timeout":
		return &slackRetryError{cause: ErrUnavailable, method: method}
	default:
		return fmt.Errorf("%w: %s failed (%s)", ErrUnavailable, method, code)
	}
}
