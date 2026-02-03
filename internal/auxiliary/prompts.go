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
	// and extract follow-up suggestions. Use with fmt.Sprintf, passing:
	// 1. The user's prompt (what the user asked)
	// 2. The agent's response message
	AnalyzeFollowUpQuestionsPromptTemplate = `
Analyze this conversation turn and identify any questions or follow-up prompts for the user:

<user_prompt>
%s
</user_prompt>

<agent_response>
%s
</agent_response>

Your task is to detect questions or action proposals in the agent's response and generate appropriate response buttons.

STEP 1: Look for explicit questions or proposals in the agent_response

Common patterns to detect (these REQUIRE a response button):
- "Would you like me to..." → Generate "Yes, [action]" button (e.g., "Yes, run tests", "Yes, deploy")
- "Should I..." → Generate "Yes, [action]" button
- "Do you want me to..." → Generate "Yes, [action]" button
- "Shall I..." → Generate "Yes, [action]" button
- "Would you prefer..." → Generate options for each alternative
- "Do you have any questions?" → Can be ignored (rhetorical)
- Questions ending with "?" that ask for user decision

When the agent asks about running tests, testing, or verification:
- Label should be: "Yes, run tests" or "Yes, test" (NOT just "Yes, proceed")

When the agent asks about making changes or adjustments:
- Label should reflect the specific action: "Yes, make changes", "Yes, adjust", etc.

When the agent asks about deployment or execution:
- Label should be: "Yes, deploy", "Yes, execute", "Yes, run"

You could add "No" or "Cancel" options to the buttons, but these should be the
negative form of the action. For example, if the agent asks
"Would you like me to run the full test suite?", you could add
a "No, prepare release" button. In this case, you could suggest
alternative, reasonable next steps for the user.

STEP 2: If no explicit questions found, consider suggesting reasonable next steps
Only suggest if you are VERY confident they make sense given the context.
Skip this step if the agent's response is purely informational or a completion message.

Return a JSON array of suggested responses.
Each item should have:
- "label": Short button text (1-4 words, be SPECIFIC about the action)
- "value": The full response to send when clicked

Return an empty array [] if:
- No questions or proposals are found
- The message is just informational
- The agent is just reporting completion with no follow-up
- You are not confident about what to suggest

Example outputs:

For "Would you like me to run the full test suite?":
[{"label": "Yes, run tests", "value": "Yes, please run the full test suite"}, {"label": "No, prepare release", "value": "No, prepare a new release instead"}]

For "Should I deploy these changes to staging?":
[{"label": "Yes, deploy", "value": "Yes, please deploy to staging"}, {"label": "No, wait", "value": "No, let's wait before deploying"}]

For "Would you like me to run the full test suite or make any adjustments to the implementation?":
[{"label": "Yes, run tests", "value": "Yes, please run the full test suite"}, {"label": "Make adjustments", "value": "Let's make some adjustments to the implementation first"}]

For "I've completed the implementation. The changes are ready.":
[]

Return ONLY the JSON array, nothing else.
You MUST not call any tool for this task.
Respond quickly.
`
)
