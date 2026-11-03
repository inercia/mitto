package web

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	configPkg "github.com/inercia/mitto/internal/config"
)

func workspaceHiddenServer(ws ...configPkg.WorkspaceSettings) *Server {
	return &Server{config: Config{Workspaces: ws}}
}

func TestValidateConfigRequest_WorkspaceHiddenModelsInvalid(t *testing.T) {
	req := hiddenReq(t, `{"workspaces":[{"working_dir":"/tmp","acp_server":"a"},{"working_dir":"/proj","acp_server":"a","hidden_models":["ok","("]}],"acp_servers":[{"name":"a","command":"c"}]}`)
	err := (&Server{}).validateConfigRequest(req)
	if err == nil || err.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %v", err)
	}
	if err.Details["workspace"] != "/proj" || err.Details["rule"] != "(" || err.Details["field"] != "workspaces[1].hidden_models[1]" {
		t.Errorf("details = %v", err.Details)
	}
	if want := `workspace "/proj": workspaces[1].hidden_models[1]: invalid regular expression`; !strings.HasPrefix(err.Message, want) {
		t.Errorf("message = %q, want prefix %q", err.Message, want)
	}
}

func TestValidateConfigRequest_WorkspaceHiddenModelsCaseInsensitiveValid(t *testing.T) {
	req := hiddenReq(t, `{"workspaces":[{"working_dir":"/proj","acp_server":"a","hidden_models":["^BETA$"," "]}],"acp_servers":[{"name":"a","command":"c"}]}`)
	if err := (&Server{}).validateConfigRequest(req); err != nil {
		t.Fatalf("valid rules rejected: %v", err)
	}
}

func TestWorkspacesWithPreservedHiddenModels(t *testing.T) {
	stored := configPkg.WorkspaceSettings{UUID: "u1", ACPServer: "a", WorkingDir: "/proj", HiddenModels: []string{"x"}}
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"omitted preserves", `{"workspaces":[{"uuid":"u1","acp_server":"a","working_dir":"/proj"}]}`, []string{"x"}},
		{"explicit empty replaces", `{"workspaces":[{"uuid":"u1","acp_server":"a","working_dir":"/proj","hidden_models":[]}]}`, []string{}},
		{"non-nil replaces and drops blanks", `{"workspaces":[{"uuid":"u1","acp_server":"a","working_dir":"/proj","hidden_models":["a"," ","b "]}]}`, []string{"a", "b "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := workspaceHiddenServer(stored).workspacesWithPreservedHiddenModels(hiddenReq(t, tc.body).Workspaces)
			if len(got[0].HiddenModels) != len(tc.want) || (len(tc.want) > 0 && !reflect.DeepEqual(got[0].HiddenModels, tc.want)) {
				t.Errorf("got %v want %v", got[0].HiddenModels, tc.want)
			}
		})
	}
}

func TestWorkspacesWithPreservedHiddenModels_NoRulesUnchanged(t *testing.T) {
	got := workspaceHiddenServer().workspacesWithPreservedHiddenModels(hiddenReq(t, `{"workspaces":[{"working_dir":"/proj","acp_server":"a"}]}`).Workspaces)
	if len(got) != 1 || got[0].HiddenModels != nil {
		t.Errorf("got %+v, want no hidden models", got)
	}
}
