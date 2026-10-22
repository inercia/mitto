package web

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/inercia/mitto/internal/config"
)

func hiddenReq(t *testing.T, body string) *ConfigSaveRequest {
	t.Helper()
	var req ConfigSaveRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	return &req
}

func hiddenServer(rules ...string) *Server {
	return &Server{config: Config{MittoConfig: &config.Config{
		ACPServers: []config.ACPServer{{Name: "old", Command: "c", HiddenModels: rules}},
	}}}
}

func TestValidateConfigRequest_HiddenModelsInvalid(t *testing.T) {
	req := hiddenReq(t, `{"workspaces":[{"working_dir":"/tmp","acp_server":"a"}],"acp_servers":[{"name":"a","command":"c","hidden_models":["ok","("]}]}`)
	err := (&Server{}).validateConfigRequest(req)
	if err == nil || err.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %v", err)
	}
	if err.Details["server"] != "a" || err.Details["rule"] != "(" || err.Details["field"] != "acp_servers[0].hidden_models[1]" {
		t.Errorf("details = %v", err.Details)
	}
}

func TestBuildNewSettings_HiddenModels(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"omitted preserves", `{"acp_servers":[{"name":"old","command":"c"}]}`, []string{"x"}},
		{"rename preserves", `{"acp_servers":[{"name":"new","command":"c"}],"server_renames":{"old":"new"}}`, []string{"x"}},
		{"explicit empty clears", `{"acp_servers":[{"name":"old","command":"c","hidden_models":[]}]}`, nil},
		{"replace drops blanks", `{"acp_servers":[{"name":"old","command":"c","hidden_models":["a"," ","b "]}]}`, []string{"a", "b "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := hiddenServer("x").buildNewSettings(hiddenReq(t, tc.body))
			if err != nil {
				t.Fatal(err)
			}
			got := s.ACPServers[0].HiddenModels
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}
