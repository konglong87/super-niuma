# Feishu AskUserQuestion

Feishu channel supports durable `AskUserQuestion` interactions when
`GOLANG_CC_CHANNEL_QUESTIONS=on` (the default).

When the model asks a question with choices, the worker sends a Card 2.0 card
with one standalone callback button per choice. The user can also send a text
message in the same chat. The answer is bound to the original tenant, account,
conversation and user; another user cannot answer it.

The run enters `waiting_input` and does not issue another model request while
the question is pending. After a button or text answer is accepted, the same
run resumes with the original assistant `tool_use` and a real `tool_result`.

Answers are one-shot and expire after the configured interaction timeout. A
duplicate callback is acknowledged without rerunning the model. `/stop`
cancels the waiting run; `/status` reports the channel run state.

The interaction request and model resume checkpoint are encrypted in
`channel_interactions`. Logs contain only interaction/run IDs and lifecycle
status, never question text, answer text, or callback tokens.

## Disabled mode

Set `GOLANG_CC_CHANNEL_QUESTIONS=off` to fail closed during rollout. The worker
will not allow the model to guess an answer; it returns an explicit channel
failure card instead.

## Verification

For a real worker, verify the following rows after asking and answering:

- `channel_interactions`: pending, then answered;
- `channel_runs`: running -> waiting_input -> queued -> completed;
- `channel_outbox`: one question card and one final card per logical run;
- `tenant_session_messages`: the final assistant answer uses a new turn index;
- worker logs: `channel.interaction.created` and `channel.interaction.answered`.
