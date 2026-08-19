package slackcatalog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSlackClientValidationAndPaginatedChannels(t *testing.T) {
	const appToken = "xapp-1-A123-opaque"
	const botToken = "xoxb-secret"
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, strings.TrimPrefix(r.URL.Path, "/"))
		wantToken := botToken
		if r.URL.Path == "/apps.connections.open" {
			wantToken = appToken
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
			t.Errorf("Authorization = %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/apps.connections.open":
			fmt.Fprint(w, `{"ok":true,"url":"wss://example.invalid/socket"}`)
		case "/auth.test":
			fmt.Fprint(w, `{"ok":true,"team_id":"T123","team":"Example","bot_id":"B123","user_id":"U123"}`)
		case "/bots.info":
			if r.Form.Get("bot") != "B123" || r.Form.Get("team_id") != "T123" {
				t.Errorf("bots.info form = %v", r.Form)
			}
			fmt.Fprint(w, `{"ok":true,"bot":{"app_id":"A123"}}`)
		case "/conversations.list":
			if r.Form.Get("types") != "public_channel,private_channel" || r.Form.Get("limit") != "25" || r.Form.Get("cursor") != "cursor-1" {
				t.Errorf("conversations.list form = %v", r.Form)
			}
			fmt.Fprint(w, `{"ok":true,"channels":[{"id":"C1","name":"general","is_member":true},{"id":"G1","name":"private-ops","is_private":true,"is_member":true},{"id":"C2","name":"old","is_archived":true},{"id":"","name":"bad"}],"response_metadata":{"next_cursor":"cursor-2"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &SlackClient{APIURL: server.URL, Client: server.Client()}
	if appID, err := client.ValidateApp(context.Background(), appToken); err != nil || appID != "A123" {
		t.Fatalf("ValidateApp() = %q, %v", appID, err)
	}
	identity, err := client.ValidateInstallation(context.Background(), botToken)
	if err != nil || identity.CredentialKind != CredentialKindBot || identity.SlackAppID != "A123" ||
		identity.TeamID != "T123" || identity.BotUserID != "U123" {
		t.Fatalf("ValidateInstallation() = %#v, %v", identity, err)
	}
	page, err := client.ListChannels(context.Background(), botToken, "cursor-1", 25)
	if err != nil || len(page.Channels) != 2 || page.Channels[0].ID != "C1" || !page.Channels[0].IsMember ||
		page.Channels[0].IsPrivate || page.Channels[1].ID != "G1" || !page.Channels[1].IsPrivate ||
		!page.Channels[1].IsMember || page.NextCursor != "cursor-2" {
		t.Fatalf("ListChannels() = %#v, %v", page, err)
	}
	wantMethods := []string{"apps.connections.open", "auth.test", "bots.info", "conversations.list"}
	if fmt.Sprint(methods) != fmt.Sprint(wantMethods) {
		t.Fatalf("methods = %v, want %v", methods, wantMethods)
	}
}

func TestSlackClientClassifiesDelegatedUserFromAuthTest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth.test" {
			t.Fatalf("unexpected Slack method %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"ok":true,"app_id":"A123","team_id":"T123","team":"Example","user_id":"U456"}`)
	}))
	defer server.Close()

	client := &SlackClient{APIURL: server.URL, Client: server.Client()}
	identity, err := client.ValidateInstallation(context.Background(), "xoxp-user-secret")
	if err != nil || identity.CredentialKind != CredentialKindUser || identity.SlackAppID != "A123" ||
		identity.TeamID != "T123" || identity.UserID != "U456" || identity.BotID != "" {
		t.Fatalf("ValidateInstallation() = %#v, %v", identity, err)
	}
}

func TestSlackClientRejectsDelegatedUserWithoutProvenAppIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"ok":true,"team_id":"T123","user_id":"U456"}`)
	}))
	defer server.Close()

	client := &SlackClient{APIURL: server.URL, Client: server.Client()}
	_, err := client.ValidateInstallation(context.Background(), "xoxp-user-secret")
	if !errors.Is(err, ErrConflict) || strings.Contains(err.Error(), "xoxp-user-secret") {
		t.Fatalf("missing app identity error = %v", err)
	}
}

func TestSlackClientClassifiesRevokedAndDeactivatedAuthorizationAsInvalid(t *testing.T) {
	for _, code := range []string{"token_revoked", "token_expired", "account_inactive"} {
		t.Run(code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `{"ok":false,"error":%q}`, code)
			}))
			defer server.Close()
			client := &SlackClient{APIURL: server.URL, Client: server.Client()}
			if _, err := client.ValidateInstallation(context.Background(), "xoxp-user-secret"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("ValidateInstallation() error = %v", err)
			}
		})
	}
}

func TestSlackClientRejectsMalformedAndFailedValidationWithoutLeakingToken(t *testing.T) {
	if _, err := NewSlackClient().ValidateApp(context.Background(), "bad"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed app token error = %v", err)
	}
	if _, err := NewSlackClient().ValidateInstallation(context.Background(), "bad"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("malformed bot token error = %v", err)
	}
	const token = "xoxb-super-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"ok":false,"error":"invalid_auth"}`)
	}))
	defer server.Close()
	client := &SlackClient{APIURL: server.URL, Client: server.Client()}
	_, err := client.ValidateInstallation(context.Background(), token)
	if !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), token) {
		t.Fatalf("failed validation error = %q", err)
	}
}

func TestSlackClientClassifiesTransientAPIFailureAsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"ok":false,"error":"ratelimited"}`)
	}))
	defer server.Close()
	client := &SlackClient{APIURL: server.URL, Client: server.Client()}
	_, err := client.ValidateInstallation(context.Background(), "xoxb-token")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("transient validation error = %v", err)
	}
}

func TestSlackClientSendsFormEncodedValues(t *testing.T) {
	values := make(chan url.Values, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		values <- r.Form
		fmt.Fprint(w, `{"ok":true,"channels":[]}`)
	}))
	defer server.Close()
	client := &SlackClient{APIURL: server.URL, Client: server.Client()}
	if _, err := client.ListChannels(context.Background(), "xoxb-token", "a+b", 100); err != nil {
		t.Fatal(err)
	}
	if got := (<-values).Get("cursor"); got != "a+b" {
		t.Fatalf("cursor = %q", got)
	}
}
