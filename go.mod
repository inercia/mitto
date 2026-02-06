module github.com/inercia/mitto

go 1.24.0

require (
	github.com/coder/acp-go-sdk v0.6.3
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/keybase/go-keychain v0.0.1
	github.com/microcosm-cc/bluemonday v1.0.27
	github.com/reeflective/readline v1.1.4
	github.com/spf13/cobra v1.8.1
	github.com/webview/webview_go v0.0.0-20240831120633-6173450d4dd6
	github.com/yuin/goldmark v1.7.16
	github.com/yuin/goldmark-highlighting/v2 v2.0.0-20230729083705-37449abec8cc
	golang.org/x/time v0.14.0
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/alecthomas/chroma/v2 v2.2.0 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/dlclark/regexp2 v1.7.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/rivo/uniseg v0.4.4 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/net v0.26.0 // indirect
	golang.org/x/sys v0.21.0 // indirect
)

// Once https://github.com/coder/acp-go-sdk/pull/18 is merged into coder/acp-go-sdk
// and a new version is released, you can remove the replace directive and update to the official version.
replace github.com/coder/acp-go-sdk => github.com/agentcooper/acp-go-sdk v0.0.0-20260130133646-65ae55c285fb
