module github.com/inercia/mitto

go 1.25.5

require (
	github.com/coder/acp-go-sdk v0.6.4-0.20260227160919-584abe6abe22
	github.com/coreos/go-oidc/v3 v3.17.0
	github.com/fsnotify/fsnotify v1.9.0
	github.com/go-jose/go-jose/v4 v4.1.3
	github.com/google/cel-go v0.27.0
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/inercia/go-restricted-runner v0.2.0
	github.com/keybase/go-keychain v0.0.1
	github.com/microcosm-cc/bluemonday v1.0.27
	github.com/modelcontextprotocol/go-sdk v1.4.0
	github.com/reeflective/readline v1.1.4
	github.com/spf13/cobra v1.10.2
	github.com/webview/webview_go v0.0.0-20240831120633-6173450d4dd6
	github.com/yuin/goldmark v1.7.16
	github.com/yuin/goldmark-highlighting/v2 v2.0.0-20230729083705-37449abec8cc
	go.abhg.dev/goldmark/mermaid v0.6.0
	golang.org/x/time v0.14.0
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	cel.dev/expr v0.25.1 // indirect
	dario.cat/mergo v1.0.2 // indirect
	github.com/Masterminds/goutils v1.1.1 // indirect
	github.com/Masterminds/semver/v3 v3.4.0 // indirect
	github.com/Masterminds/sprig/v3 v3.3.0 // indirect
	github.com/alecthomas/chroma/v2 v2.23.1 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/huandu/xstrings v1.5.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.3 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240826202546-f6391c0de4c7 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240826202546-f6391c0de4c7 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
)

// Once https://github.com/coder/acp-go-sdk/pull/18 is merged into coder/acp-go-sdk
// and a new version is released, you can remove the replace directive and update to the official version.
// replace github.com/coder/acp-go-sdk => github.com/agentcooper/acp-go-sdk v0.0.0-20260130133646-65ae55c285fb
