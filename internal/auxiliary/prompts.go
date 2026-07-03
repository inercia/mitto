package auxiliary

import _ "embed"

// Prompt templates used by the auxiliary conversation for various tasks.
// The template bodies live as individual files under prompts/ and are embedded
// at build time via Go's embed directive.

// GenerateTitlePromptTemplate is used to generate a short title for a conversation
// based on the initial message. Use with fmt.Sprintf, passing the initial message.
//
//go:embed prompts/generate_title.txt
var GenerateTitlePromptTemplate string

// GenerateQueuedMessageTitlePromptTemplate is used to generate a short title
// for a queued message. Use with fmt.Sprintf, passing the message.
//
//go:embed prompts/generate_queued_message_title.txt
var GenerateQueuedMessageTitlePromptTemplate string

// ImprovePromptTemplate is used to enhance a user's prompt to make it
// clearer, more specific, and more effective. Use with fmt.Sprintf,
// passing the original user prompt.
//
//go:embed prompts/improve_prompt.txt
var ImprovePromptTemplate string

// AnalyzeFollowUpQuestionsPromptTemplate is used to analyze an agent message
// and extract follow-up suggestions. Use with fmt.Sprintf, passing:
// 1. The user's prompt (what the user asked)
// 2. The agent's response message
//
//go:embed prompts/analyze_followup_questions.txt
var AnalyzeFollowUpQuestionsPromptTemplate string

// FetchMCPToolsPromptTemplate asks the agent for all its available tools.
// This prompt requires no parameters (do not use fmt.Sprintf).
//
//go:embed prompts/fetch_mcp_tools.txt
var FetchMCPToolsPromptTemplate string

// CheckToolPatternsPromptTemplate asks the agent to check if specific tool patterns
// have matching MCP tools available. Use with fmt.Sprintf, passing the comma-separated patterns.
// This is sent to the same PurposeMCPTools auxiliary session, so the agent already has
// context from the initial FetchMCPTools query.
//
//go:embed prompts/check_tool_patterns.txt
var CheckToolPatternsPromptTemplate string

// CheckMCPAvailabilityPromptTemplate is used to verify if Mitto MCP tools are available.
// Use with fmt.Sprintf, passing the MCP server URL.
//
//go:embed prompts/check_mcp_availability.txt
var CheckMCPAvailabilityPromptTemplate string
