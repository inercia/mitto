package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestListSlackChannelsDecodesModeScopedPages(t *testing.T) {
	server := newFakeServer(t)
	for _, test := range []struct {
		name        string
		installID   string
		credential  string
		response    string
		wantID      string
		wantName    string
		wantPrivate bool
		wantMember  bool
		wantCursor  string
	}{
		{name: "bot", installID: "bot-install", credential: "xoxb-sdk-secret",
			response: `{"channels":[{"id":"C-BOT","name":"bot-public","is_private":false,"is_member":true}],"next_cursor":"bot-next"}`,
			wantID:   "C-BOT", wantName: "bot-public", wantMember: true, wantCursor: "bot-next"},
		{name: "delegated user", installID: "user-install", credential: "xoxp-sdk-secret",
			response: `{"channels":[{"id":"G-USER","name":"user-private","is_private":true,"is_member":true}],"next_cursor":"user-next"}`,
			wantID:   "G-USER", wantName: "user-private", wantPrivate: true, wantMember: true, wantCursor: "user-next"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := "/mitto/api/slack/installations/" + test.installID + "/channels"
			server.On(http.MethodGet, path).RespondJSON(http.StatusOK, test.response)
			page, err := server.Client().ListSlackChannels(test.installID, "cursor value", 25)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Channels) != 1 || page.Channels[0].ID != test.wantID ||
				page.Channels[0].Name != test.wantName || page.Channels[0].IsPrivate != test.wantPrivate ||
				page.Channels[0].IsMember != test.wantMember || page.NextCursor != test.wantCursor {
				t.Fatalf("channel page = %#v", page)
			}
			encoded, err := json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), test.credential) {
				t.Fatalf("channel page leaked installation credential: %s", encoded)
			}
			request := server.LastRequest()
			if request.Method != http.MethodGet || request.Path != path {
				t.Fatalf("request = %s %s", request.Method, request.Path)
			}
			query, err := url.ParseQuery(request.RawQuery)
			if err != nil || query.Get("cursor") != "cursor value" || query.Get("limit") != "25" {
				t.Fatalf("query = %q, err=%v", request.RawQuery, err)
			}
		})
	}
}
