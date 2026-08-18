package slackcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var appTokenPattern = regexp.MustCompile(`^xapp-[0-9]+-(A[A-Z0-9]+)-`)

const slackRequestTimeout = 30 * time.Second

type SlackProvider interface {
	ValidateApp(context.Context, string) (string, error)
	ValidateInstallation(context.Context, string) (InstallationIdentity, error)
	ListChannels(context.Context, string, string, int) (ChannelPage, error)
}

type SlackClient struct {
	APIURL string
	Client *http.Client
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
	if !strings.HasPrefix(strings.TrimSpace(token), "xoxb-") {
		return InstallationIdentity{}, fmt.Errorf("%w: malformed bot token", ErrInvalid)
	}
	var auth struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
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
	if auth.TeamID == "" || auth.BotID == "" || auth.UserID == "" {
		return InstallationIdentity{}, fmt.Errorf("%w: auth.test omitted required identity fields", ErrConflict)
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
	return InstallationIdentity{SlackAppID: bot.Bot.AppID, TeamID: auth.TeamID, TeamName: auth.Team,
		BotID: auth.BotID, BotUserID: auth.UserID}, nil
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
	var response struct {
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
	if err := c.call(ctx, token, "conversations.list", values, &response); err != nil {
		return ChannelPage{}, err
	}
	if !response.OK {
		return ChannelPage{}, slackAPIError("conversations.list", response.Error)
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
	default:
		return fmt.Errorf("%w: %s failed (%s)", ErrUnavailable, method, code)
	}
}
