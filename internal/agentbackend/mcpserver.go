package agentbackend

// MCPServerDescriptor is a protocol-neutral description of one MCP server
// entry passed to a session-establishment call (NewSession/LoadSession/
// ResumeSession), mirroring the shape of the ACP SDK's own McpServer
// transport-variant union (mitto-mx9.1.2). Exactly one of Stdio/HTTP is set
// per entry, mirroring how ACP's own union works (only one of its
// Http/Sse/Acp/Stdio fields is populated per entry). Sse/Acp variants are not
// modeled here — Mitto's production code only ever constructs Stdio (the
// auxiliary MCP proxy fallback, mitto-8ip) or HTTP (the mitto-apvg native
// binding) entries; add a variant here if a real caller needs one.
type MCPServerDescriptor struct {
	// Stdio configures a stdio-transport MCP server. All ACP agents MUST
	// support this transport.
	Stdio *MCPServerStdio
	// HTTP configures an HTTP-transport MCP server, only available when the
	// agent advertises mcp_capabilities.http support. The mitto-apvg
	// per-conversation binding token rides in a Headers entry here.
	HTTP *MCPServerHTTP
}

// MCPServerStdio describes a stdio-transport MCP server entry.
type MCPServerStdio struct {
	// Name is a human-readable identifier for this MCP server.
	Name string
	// Command is the path to the MCP server executable.
	Command string
	// Args are the command-line arguments passed to Command.
	Args []string
	// Env are the environment variables set when launching Command.
	Env []EnvVar
}

// MCPServerHTTP describes an HTTP-transport MCP server entry.
type MCPServerHTTP struct {
	// Name is a human-readable identifier for this MCP server.
	Name string
	// URL is the MCP server's HTTP endpoint.
	URL string
	// Headers are the HTTP headers set on every request to URL.
	Headers []HTTPHeader
}

// EnvVar is a single environment variable name/value pair, mirroring the ACP
// SDK's EnvVariable.
type EnvVar struct {
	Name  string
	Value string
}

// HTTPHeader is a single HTTP header name/value pair, mirroring the ACP
// SDK's HttpHeader. Kept as an ordered slice element (not a map) on
// MCPServerHTTP.Headers so entry order and duplicate names survive
// round-tripping through the ACP SDK's own []HttpHeader shape.
type HTTPHeader struct {
	Name  string
	Value string
}
