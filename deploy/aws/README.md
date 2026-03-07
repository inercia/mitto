# AWS Deployment Resources

This directory contains AWS IAM policies and related infrastructure resources for Mitto's Bedrock integration.

## Files

### `bedrock-api-key-policy.json`

IAM identity-based policy to attach to the `BedrockAPIKey-*` IAM user that Mitto uses to invoke Claude models via Amazon Bedrock.

**Allowed models:**
- `anthropic.claude-opus-4*` — Used for the main conversation (e.g. "Claude Code (Opus 4.6)")
- `anthropic.claude-sonnet-4*` — Used for sub-agent task spawning (Bash, Explore, Plan agents)
- `anthropic.claude-haiku-4*` — Used for lightweight/fast sub-agent tasks

Both `bedrock:InvokeModel` and `bedrock:InvokeModelWithResponseStream` are required:
- `InvokeModel` — synchronous inference
- `InvokeModelWithResponseStream` — streaming inference (used by Claude Code and Mitto)

## Deploying the Policy

To update the policy attached to the IAM user in AWS:

```bash
# Replace POLICY_ARN with the ARN of the existing managed policy
aws iam create-policy-version \
  --policy-arn <POLICY_ARN> \
  --policy-document file://bedrock-api-key-policy.json \
  --set-as-default

# Or, if attaching inline to the user directly:
aws iam put-user-policy \
  --user-name BedrockAPIKey-9hjj \
  --policy-name BedrockModelAccess \
  --policy-document file://bedrock-api-key-policy.json
```

## Why Sub-agents Need Sonnet/Haiku

When Mitto runs the `Task` tool to spawn specialized agents (Bash, Explore, Plan, general-purpose),
those agents may use a different model than the main conversation. Specifically:

- **Main conversation**: `claude-opus-4*` (configured in the ACP server)
- **Sub-agents**: `claude-sonnet-4*` or `claude-haiku-4*` (used by the Task tool for cost efficiency)

If the IAM policy only allows Opus, sub-agent invocations will fail with a 403 error, surfaced
in Mitto logs as `msg=prompt_failed` with `"Failed to authenticate"`.
