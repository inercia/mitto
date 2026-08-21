package slackcatalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type slackRoundTripFunc func(*http.Request) (*http.Response, error)

func (f slackRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

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

func TestSlackClientListChannelsUsesInstallationCredentialByMode(t *testing.T) {
	for _, test := range []struct {
		name  string
		token string
		id    string
		label string
	}{
		{name: "bot", token: "xoxb-bot-secret", id: "G-BOT", label: "bot-private"},
		{name: "delegated user", token: "xoxp-user-secret", id: "G-USER", label: "user-private"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/conversations.list" {
					t.Fatalf("unexpected Slack method %s", r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer "+test.token {
					t.Fatalf("Authorization = %q", got)
				}
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if r.Form.Get("types") != "public_channel,private_channel" ||
					r.Form.Get("exclude_archived") != "true" || r.Form.Get("limit") != "50" ||
					r.Form.Get("cursor") != "cursor-in" {
					t.Fatalf("conversations.list form = %v", r.Form)
				}
				fmt.Fprintf(w, `{"ok":true,"channels":[{"id":%q,"name":%q,"is_private":true,"is_member":true}],"response_metadata":{"next_cursor":"cursor-out"}}`, test.id, test.label)
			}))
			defer server.Close()

			page, err := (&SlackClient{APIURL: server.URL, Client: server.Client()}).ListChannels(
				context.Background(), test.token, "cursor-in", 50,
			)
			if err != nil || len(page.Channels) != 1 || page.Channels[0].ID != test.id ||
				page.Channels[0].Name != test.label || !page.Channels[0].IsPrivate ||
				!page.Channels[0].IsMember || page.NextCursor != "cursor-out" {
				t.Fatalf("ListChannels() = %#v, %v", page, err)
			}
			if strings.Contains(fmt.Sprint(page), test.token) {
				t.Fatalf("ListChannels() returned installation credential: %#v", page)
			}
		})
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

func TestSlackClientOAuthExchangeUsesFormAndReturnsUserProvenance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth.v2.access" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" {
			t.Fatal("OAuth exchange unexpectedly used an Authorization header")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("client_id") != "123.456" || r.Form.Get("client_secret") != "client-secret" ||
			r.Form.Get("code") != "one-time-code" || r.Form.Get("redirect_uri") != "https://mitto.example/callback" {
			t.Fatalf("OAuth form = %#v", r.Form)
		}
		fmt.Fprint(w, `{"ok":true,"app_id":"A123","team":{"id":"T123","name":"Example"},"authed_user":{"id":"U456","access_token":"xoxp-user-secret"}}`)
	}))
	defer server.Close()
	identity, err := (&SlackClient{APIURL: server.URL, Client: server.Client()}).ExchangeOAuth(
		context.Background(), "123.456", "client-secret", "one-time-code", "https://mitto.example/callback",
	)
	if err != nil || identity.CredentialKind != CredentialKindUser || identity.SlackAppID != "A123" ||
		identity.TeamID != "T123" || identity.UserID != "U456" || identity.AccessToken != "xoxp-user-secret" {
		t.Fatalf("ExchangeOAuth() = %#v, %v", identity, err)
	}
}

func TestSlackClientRejectsDelegatedUserWithoutProvenAppIdentity(t *testing.T) {
	// Production-shaped response: a standard Slack user-token auth.test
	// reply, which never includes app_id (mitto-3od5.1 recurrence). This
	// must be classified as ErrOAuthRequired, a distinct value-free
	// sentinel, not the generic ErrConflict used for identity mismatches.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"ok":true,"team_id":"T123","user_id":"U456"}`)
	}))
	defer server.Close()

	client := &SlackClient{APIURL: server.URL, Client: server.Client()}
	_, err := client.ValidateInstallation(context.Background(), "xoxp-user-secret")
	if !errors.Is(err, ErrOAuthRequired) {
		t.Fatalf("missing app identity error = %v, want ErrOAuthRequired", err)
	}
	if errors.Is(err, ErrConflict) {
		t.Fatalf("missing app identity error must not also classify as ErrConflict: %v", err)
	}
	if strings.Contains(err.Error(), "xoxp-user-secret") {
		t.Fatalf("missing app identity error leaked token: %v", err)
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

func TestSlackClientClassifiesTransientAPIFailureAsRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"ok":false,"error":"ratelimited"}`)
	}))
	defer server.Close()
	client := &SlackClient{APIURL: server.URL, Client: server.Client()}
	_, err := client.ValidateInstallation(context.Background(), "xoxb-token")
	if !errors.Is(err, ErrRateLimited) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("transient validation error = %v, want distinct ErrRateLimited", err)
	}
}

func TestSlackClientListChannelsRetriesRateLimitAtSameCursor(t *testing.T) {
	// mitto-2lhp: a throttled later page must recover without making the user
	// restart discovery or losing the cursor that selected that page.
	var cursors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			return
		}
		cursors = append(cursors, r.Form.Get("cursor"))
		if len(cursors) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"ok":true,"channels":[{"id":"C2","name":"operations"}],"response_metadata":{"next_cursor":"page-3"}}`)
	}))
	defer server.Close()

	client := &SlackClient{APIURL: server.URL, Client: server.Client()}
	page, err := client.ListChannels(context.Background(), "xoxb-token", "page-2", 200)
	if err != nil {
		t.Fatalf("ListChannels() error = %v, want automatic Retry-After recovery", err)
	}
	if len(page.Channels) != 1 || page.Channels[0].ID != "C2" || page.NextCursor != "page-3" {
		t.Fatalf("ListChannels() = %#v, want recovered page", page)
	}
	if fmt.Sprint(cursors) != "[page-2 page-2]" {
		t.Fatalf("cursors = %v, want same cursor retried", cursors)
	}
}

func TestSlackClientListChannelsHonorsRetryAfter(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"ok":true,"channels":[]}`)
	}))
	defer server.Close()
	var delays []time.Duration
	client := &SlackClient{APIURL: server.URL, Client: server.Client(), sleepFn: func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}}
	if _, err := client.ListChannels(context.Background(), "xoxb-token", "next", 200); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(delays) != "[2s]" {
		t.Fatalf("retry delays = %v, want [2s]", delays)
	}
}

func TestSlackClientListChannelsUsesFallbackBackoff(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		switch calls {
		case 1:
			w.Header().Set("Retry-After", "invalid")
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			fmt.Fprint(w, `{"ok":true,"channels":[]}`)
		}
	}))
	defer server.Close()
	var delays []time.Duration
	client := &SlackClient{APIURL: server.URL, Client: server.Client(), randFn: func() float64 { return 0.5 },
		sleepFn: func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		}}
	if _, err := client.ListChannels(context.Background(), "xoxb-token", "next", 200); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(delays) != "[500ms 1s]" {
		t.Fatalf("retry delays = %v, want [500ms 1s]", delays)
	}
}

func TestSlackClientListChannelsDoesNotRetryTerminalError(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprint(w, `{"ok":false,"error":"missing_scope"}`)
	}))
	defer server.Close()
	client := &SlackClient{APIURL: server.URL, Client: server.Client()}
	_, err := client.ListChannels(context.Background(), "xoxb-token", "next", 200)
	if !errors.Is(err, ErrInvalid) || calls != 1 {
		t.Fatalf("error = %v, calls = %d; want terminal ErrInvalid with one call", err, calls)
	}
}

func TestSlackClientListChannelsCancellationStopsRetryWait(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	client := &SlackClient{APIURL: server.URL, Client: server.Client(), sleepFn: func(ctx context.Context, _ time.Duration) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}}
	_, err := client.ListChannels(ctx, "xoxb-token", "next", 200)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestSlackClientListChannelsRetriesTransportTimeout(t *testing.T) {
	var calls int
	httpClient := &http.Client{Transport: slackRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, context.DeadlineExceeded
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"ok":true,"channels":[]}`))}, nil
	})}
	client := &SlackClient{APIURL: "https://slack.invalid", Client: httpClient,
		sleepFn: func(context.Context, time.Duration) error { return nil }}
	if _, err := client.ListChannels(context.Background(), "xoxb-token", "next", 200); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want timeout retry", calls)
	}
}

func TestSlackClientListChannelsStopsAfterAttemptBudgetWithoutLeakingBody(t *testing.T) {
	const canary = "provider-body-must-not-escape"
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, canary)
	}))
	defer server.Close()
	client := &SlackClient{APIURL: server.URL, Client: server.Client(), maxAttempts: 2,
		sleepFn: func(context.Context, time.Duration) error { return nil }}
	_, err := client.ListChannels(context.Background(), "xoxb-token", "next", 200)
	if !errors.Is(err, ErrUnavailable) || calls != 2 {
		t.Fatalf("error = %v, calls = %d; want exhausted ErrUnavailable after two calls", err, calls)
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("error leaked Slack response body: %v", err)
	}
}

func TestSlackClientListChannelsDoesNotOutwaitRetryBudget(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client := &SlackClient{APIURL: server.URL, Client: server.Client(), retryBudget: 10 * time.Millisecond}
	_, err := client.ListChannels(context.Background(), "xoxb-token", "next", 200)
	if !errors.Is(err, ErrRateLimited) || calls != 1 {
		t.Fatalf("error = %v, calls = %d; want immediate budget exhaustion", err, calls)
	}
	if got := RetryAfterSeconds(err); got != 30 {
		t.Fatalf("RetryAfterSeconds() = %d, want 30", got)
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
