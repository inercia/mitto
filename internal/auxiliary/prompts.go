package auxiliary

// Prompt templates used by the auxiliary conversation for various tasks.
const (
	// GenerateTitlePromptTemplate is used to generate a short title for a conversation
	// based on the initial message. Use with fmt.Sprintf, passing the initial message.
	GenerateTitlePromptTemplate = `
Consider this initial message in a conversation with an LLM: "%s"

What title would you use for this conversation? Keep it very short, just 2 or 3 words.
Reply with ONLY the title, nothing else.
You MUST not call any tool for this task.
Respond quickly.
`

	// GenerateQueuedMessageTitlePromptTemplate is used to generate a short title
	// for a queued message. Use with fmt.Sprintf, passing the message.
	GenerateQueuedMessageTitlePromptTemplate = `
Summarize this message in 2-3 words for a queue display: "%s"

Reply with ONLY the short title, nothing else.
You MUST not call any tool for this task.
`

	// ImprovePromptTemplate is used to enhance a user's prompt to make it
	// clearer, more specific, and more effective. Use with fmt.Sprintf,
	// passing the original user prompt.
	ImprovePromptTemplate = `
The user wants to improve the following prompt.
Please enhance it by making it clearer, more specific, and more effective,
while preserving the user's intent. Consider the current project context.
Return ONLY the improved prompt text without any explanations or preamble.
You MUST not call any tool for this task.
Respond quickly.

Original prompt:
%s`

	// AnalyzeFollowUpQuestionsPromptTemplate is used to analyze an agent message
	// and extract follow-up suggestions. Use with fmt.Sprintf, passing the agent message.
	AnalyzeFollowUpQuestionsPromptTemplate = `
Analyze this agent message and identify any questions or follow-up prompts for the user:

<agent_message>
%s
</agent_message>

Then:

A) If the agent message is clearly proposing some questions or follow-ups, get those.
   Do not look further: just use the questions or follow-ups.

B) If the agent message is not proposing any questions or follow-ups,
   think about what could be reasonable next steps (taking into consideration the current status,
   the point in the conversation, the project, the user's goals, etc.) and suggest those,
   but only if you are very confident that they are relevant and make sense.

Return a JSON array of suggested responses.
Each item should have a "label" (short button text, 1-4 words) and "value" (the full response to send).
It is perfectly fine to return an empty array [] if no questions are found
or if the message is just informational
or you cannot think of any reasonable next steps
or you are not confident.

Example format:
[
  {"label": "Yes, proceed", "value": "Yes, please proceed with that approach"},
  {"label": "Show code", "value": "Show me the code changes first"}
]

Return ONLY the JSON array, nothing else.
You MUST not call any tool for this task.
Respond quickly.
`
)
