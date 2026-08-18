package api

import (
	"net/http"
	"net/url"
	"testing"
)

func TestListSlackChannelsDecodesPrivacyAndMembership(t *testing.T) {
	server := newFakeServer(t)
	server.On(http.MethodGet, "/mitto/api/slack/installations/install-one/channels").RespondJSON(http.StatusOK,
		`{"channels":[{"id":"C1","name":"general","is_private":false,"is_member":false},{"id":"G1","name":"private-ops","is_private":true,"is_member":true}],"next_cursor":"next"}`)

	page, err := server.Client().ListSlackChannels("install-one", "cursor value", 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Channels) != 2 || page.Channels[0].IsPrivate || page.Channels[0].IsMember ||
		!page.Channels[1].IsPrivate || !page.Channels[1].IsMember || page.NextCursor != "next" {
		t.Fatalf("channel page = %#v", page)
	}
	request := server.LastRequest()
	query, err := url.ParseQuery(request.RawQuery)
	if err != nil || query.Get("cursor") != "cursor value" || query.Get("limit") != "25" {
		t.Fatalf("query = %q, err=%v", request.RawQuery, err)
	}
}
