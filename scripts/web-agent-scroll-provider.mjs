#!/usr/bin/env node
import http from "node:http";

const host = process.env.WEB_AGENT_SCROLL_PROVIDER_HOST || "127.0.0.1";
const port = Number(process.env.WEB_AGENT_SCROLL_PROVIDER_PORT || 19087);
const chunkDelayMs = Number(process.env.WEB_AGENT_SCROLL_PROVIDER_CHUNK_DELAY_MS || 12);
const defaultLineCount = Number(process.env.WEB_AGENT_SCROLL_PROVIDER_LINES || 72);

function json(res, status, body) {
  const payload = JSON.stringify(body);
  res.writeHead(status, {
    "content-type": "application/json; charset=utf-8",
    "content-length": Buffer.byteLength(payload),
  });
  res.end(payload);
}

function requestBody(req) {
  return new Promise((resolve, reject) => {
    let data = "";
    req.setEncoding("utf8");
    req.on("data", chunk => {
      data += chunk;
    });
    req.on("end", () => {
      if (!data.trim()) {
        resolve({});
        return;
      }
      try {
        resolve(JSON.parse(data));
      } catch (err) {
        reject(err);
      }
    });
    req.on("error", reject);
  });
}

function latestUserText(messages) {
  for (let i = (messages || []).length - 1; i >= 0; i--) {
    const msg = messages[i];
    if (msg?.role !== "user") {
      continue;
    }
    if (typeof msg.content === "string") {
      return msg.content;
    }
    if (Array.isArray(msg.content)) {
      return msg.content.map(part => part?.text || part?.content || "").join(" ").trim();
    }
  }
  return "";
}

function scrollText(prompt, lineCount) {
  const lines = [
    "## scroll stub: Web Agent message overflow test",
    `prompt: ${prompt || "(empty)"}`,
    "",
    "> This deterministic response validates Markdown rendering, auto-scroll, and composer recovery.",
    "",
    "- [x] heading rendering",
    "- [x] blockquote rendering",
    "- [x] task list rendering",
    "- inline **bold** and `code` rendering",
    "",
    "```ts",
    "console.log(\"web agent markdown scroll smoke\");",
    "```",
    "",
    "---",
    "",
    "This response is intentionally long and deterministic.",
  ];
  for (let i = 1; i <= lineCount; i++) {
    const padded = String(i).padStart(2, "0");
    lines.push(`line ${padded}: user/assistant bubble alignment, auto-scroll, and composer state remain stable during streaming.`);
  }
  lines.push("scroll stub: completed");
  return `${lines.join("\n")}\n`;
}

function writeSSE(res, payload) {
  res.write(`data: ${JSON.stringify(payload)}\n\n`);
}

async function handleChatCompletions(req, res) {
  let body;
  try {
    body = await requestBody(req);
  } catch (err) {
    json(res, 400, { error: { message: err.message || String(err) } });
    return;
  }
  const model = body.model || "scroll-stub";
  const text = scrollText(latestUserText(body.messages), Number(body.metadata?.line_count || defaultLineCount));
  if (!body.stream) {
    json(res, 200, {
      id: `chatcmpl-scroll-${Date.now()}`,
      object: "chat.completion",
      model,
      choices: [{ index: 0, message: { role: "assistant", content: text }, finish_reason: "stop" }],
      usage: { prompt_tokens: 16, completion_tokens: text.length, total_tokens: text.length + 16 },
    });
    return;
  }

  res.writeHead(200, {
    "content-type": "text/event-stream; charset=utf-8",
    "cache-control": "no-cache",
    connection: "keep-alive",
  });
  const id = `chatcmpl-scroll-${Date.now()}`;
  writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { role: "assistant" } }] });
  const chunks = text.match(/.{1,42}/gs) || [text];
  for (const chunk of chunks) {
    writeSSE(res, { id, object: "chat.completion.chunk", model, choices: [{ index: 0, delta: { content: chunk } }] });
    await new Promise(resolve => setTimeout(resolve, chunkDelayMs));
  }
  writeSSE(res, {
    id,
    object: "chat.completion.chunk",
    model,
    choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
    usage: { prompt_tokens: 16, completion_tokens: text.length, total_tokens: text.length + 16 },
  });
  res.write("data: [DONE]\n\n");
  res.end();
}

const server = http.createServer((req, res) => {
  if (req.method === "GET" && req.url === "/health") {
    json(res, 200, { ok: true, provider: "web-agent-scroll-stub" });
    return;
  }
  if (req.method === "GET" && req.url === "/v1/models") {
    json(res, 200, { object: "list", data: [{ id: "scroll-stub", object: "model" }] });
    return;
  }
  if (req.method === "POST" && req.url === "/v1/chat/completions") {
    void handleChatCompletions(req, res);
    return;
  }
  json(res, 404, { error: { message: `not found: ${req.method} ${req.url}` } });
});

server.listen(port, host, () => {
  console.log(`web-agent-scroll-provider: http://${host}:${port}`);
});
