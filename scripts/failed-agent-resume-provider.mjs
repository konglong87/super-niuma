#!/usr/bin/env node
import http from "node:http";
import fs from "node:fs";

const host = process.env.FAILED_AGENT_RESUME_PROVIDER_HOST || "127.0.0.1";
const portArgIndex = process.argv.indexOf("--port");
const requestedPort = portArgIndex >= 0 ? Number(process.argv[portArgIndex + 1]) : Number(process.env.FAILED_AGENT_RESUME_PROVIDER_PORT || 0);
const readyFile = process.env.FAILED_AGENT_RESUME_PROVIDER_READY || "";
const requestLog = process.env.FAILED_AGENT_RESUME_PROVIDER_LOG || "";

function writeLog(entry) {
  if (!requestLog) return;
  fs.appendFileSync(requestLog, `${JSON.stringify({ time: new Date().toISOString(), ...entry })}\n`);
}

function json(res, status, payload) {
  res.writeHead(status, { "content-type": "application/json" });
  res.end(JSON.stringify(payload));
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    let body = "";
    req.setEncoding("utf8");
    req.on("data", chunk => {
      body += chunk;
    });
    req.on("end", () => resolve(body));
    req.on("error", reject);
  });
}

function writeSSE(res, payload) {
  res.write(`data: ${JSON.stringify(payload)}\n\n`);
}

function streamText(res, model, text) {
  res.writeHead(200, {
    "content-type": "text/event-stream; charset=utf-8",
    "cache-control": "no-cache",
    connection: "keep-alive",
  });
  const id = `chatcmpl-failed-agent-resume-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: text } }] });
  writeSSE(res, {
    id,
    object: "chat.completion.chunk",
    model,
    choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
    usage: { prompt_tokens: 32, completion_tokens: text.length, total_tokens: text.length + 32 },
  });
  res.write("data: [DONE]\n\n");
  res.end();
}

function streamToolCall(res, model, id, name, args) {
  res.writeHead(200, {
    "content-type": "text/event-stream; charset=utf-8",
    "cache-control": "no-cache",
    connection: "keep-alive",
  });
  const completionID = `chatcmpl-failed-agent-resume-${Date.now()}`;
  writeSSE(res, {
    id: completionID,
    object: "chat.completion.chunk",
    model,
    choices: [{
      index: 0,
      delta: {
        role: "assistant",
        tool_calls: [{
          index: 0,
          id,
          type: "function",
          function: { name, arguments: JSON.stringify(args) },
        }],
      },
    }],
  });
  writeSSE(res, {
    id: completionID,
    object: "chat.completion.chunk",
    model,
    choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }],
    usage: { prompt_tokens: 48, completion_tokens: 12, total_tokens: 60 },
  });
  res.write("data: [DONE]\n\n");
  res.end();
}

function streamSubagentFailure(res, model) {
  res.writeHead(200, {
    "content-type": "text/event-stream; charset=utf-8",
    "cache-control": "no-cache",
    connection: "keep-alive",
  });
  const id = `chatcmpl-failed-agent-resume-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  writeSSE(res, {
    id,
    object: "chat.completion.chunk",
    model,
    choices: [{ index: 0, delta: { content: `FAILED_AGENT_RESUME_FAILED_OUTPUT_FILE_MARKER: provider stream failed after partial content

Evidence:
- FAILED_AGENT_RESUME_PARTIAL_EVIDENCE: output_file captured the provider failure marker before stream failure.

Assumptions:
- FAILED_AGENT_RESUME_ASSUMPTION: parent resumes from the same session transcript.

Unknowns:
- FAILED_AGENT_RESUME_UNKNOWN: the child stream ended before a final answer.

Verification:
- FAILED_AGENT_RESUME_VERIFICATION: scripts/failed-agent-resume-acceptance.sh checks AgentGet and post-AgentGet resume.

Risks:
- FAILED_AGENT_RESUME_RISK: partial evidence may be incomplete.

Next action:
- FAILED_AGENT_RESUME_NEXT_ACTION: explain the failure using partial evidence and disclose remaining unknowns.` } }],
  });
  res.write("data: {malformed-json-to-force-stream-error\n\n");
  res.end();
}

function requestText(body) {
  return JSON.stringify(body);
}

function toolNames(body) {
  return (body.tools || []).map(tool => tool?.function?.name || tool?.name || "").filter(Boolean);
}

function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

async function handleChatCompletions(req, res) {
  let parsed;
  try {
    parsed = JSON.parse(await readBody(req));
  } catch (err) {
    json(res, 400, { error: { message: `invalid json: ${err.message}` } });
    return;
  }
  const model = parsed.model || "failed-agent-resume-stub";
  const text = requestText(parsed);
  const tools = toolNames(parsed);
  writeLog({
    path: req.url,
    model,
    tools,
    has_subagent_prompt: text.includes("FAILED_AGENT_RESUME_SUBAGENT_PROMPT"),
    has_failure_notification: text.includes("failure notification"),
    has_agentget_result: text.includes("FAILED_AGENT_RESUME_FAILED_OUTPUT_FILE_MARKER"),
  });

  if (text.includes("FAILED_AGENT_RESUME_SUBAGENT_PROMPT") && !text.includes("FAILED_AGENT_RESUME_FIRST_PROMPT")) {
    streamSubagentFailure(res, model);
    return;
  }
  if (text.includes("FAILED_AGENT_RESUME_RESUME_PROMPT") && text.includes("failure notification") && !text.includes("call_agent_get_failed_resume")) {
    streamToolCall(res, model, "call_agent_get_failed_resume", "AgentGet", { task_id: 1 });
    return;
  }
  if (text.includes("FAILED_AGENT_RESUME_FIRST_PROMPT") && tools.includes("AgentCreate") && !text.includes("call_agent_create_failed_resume")) {
    streamToolCall(res, model, "call_agent_create_failed_resume", "AgentCreate", {
      description: "failed resume probe",
      prompt: "FAILED_AGENT_RESUME_SUBAGENT_PROMPT: emit the failure marker, then the local provider will fail the stream.",
      timeout_ms: 2000,
    });
    return;
  }
  if (text.includes("FAILED_AGENT_RESUME_FAILED_OUTPUT_FILE_MARKER")) {
    streamText(res, model, "FAILED_AGENT_RESUME_FINAL_OK: AgentGet returned failed status and the failure marker.");
    return;
  }
  if (text.includes("FAILED_AGENT_RESUME_FIRST_PROMPT") && text.includes("call_agent_create_failed_resume")) {
    await sleep(500);
  }
  streamText(res, model, "FAILED_AGENT_RESUME_IDLE_OK");
}

const server = http.createServer((req, res) => {
  if (req.method === "GET" && req.url === "/health") {
    json(res, 200, { ok: true, provider: "failed-agent-resume-stub" });
    return;
  }
  if (req.method === "GET" && req.url === "/v1/models") {
    json(res, 200, { object: "list", data: [{ id: "failed-agent-resume-stub", object: "model" }] });
    return;
  }
  if (req.method === "POST" && req.url === "/v1/chat/completions") {
    void handleChatCompletions(req, res);
    return;
  }
  json(res, 404, { error: { message: `not found: ${req.method} ${req.url}` } });
});

server.listen(Number.isFinite(requestedPort) ? requestedPort : 0, host, () => {
  const address = server.address();
  const payload = { host, port: address.port, base_url: `http://${host}:${address.port}` };
  if (readyFile) {
    fs.writeFileSync(readyFile, JSON.stringify(payload));
  }
  console.log(`failed-agent-resume-provider: ${payload.base_url}`);
});
