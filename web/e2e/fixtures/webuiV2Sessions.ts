import type { Page } from "@playwright/test";

type FixtureSession = { id: number; ref: string; source: string; title: string; status: string; updated_at: string; short_id: string; cwd: string; active_run_id?: number; model?: string; provider?: string; permission_mode?: string; effort?: string; prompt_mode?: string };
type FixtureEvent = { id: number; task_id: number; event_type: string; payload_json: string; created_at: string };

export async function installWebUIV2Sessions(page: Page): Promise<void> {
  const startTime = "2026-09-05T00:00:00.000Z";
  const managed: FixtureSession = { id: 1, ref: "tenant:alpha", source: "tenant", title: "Release coordination", status: "running", updated_at: startTime, short_id: "alpha", cwd: "/workspace/project", active_run_id: 11, provider: "openai", model: "fixture-model", permission_mode: "ask", effort: "high", prompt_mode: "code" };
  const completed: FixtureSession = { ...managed, id: 2, ref: "tenant:beta", title: "Design review", status: "completed", short_id: "beta", active_run_id: 21 };
  const local: FixtureSession = { id: 0, ref: "local:workspace", source: "local", title: "Local workspace", status: "idle", updated_at: startTime, short_id: "workspace", cwd: "/workspace/local" };
  const sessions = [managed, completed, local];
  const events = new Map<string, FixtureEvent[]>(sessions.map((session) => [session.ref, []]));
  let eventID = 0;
  let operationID = 0;
  function append(ref: string, taskID: number, type: string, payload: object) {
    events.get(ref)!.push({ id: ++eventID, task_id: taskID, event_type: type, payload_json: JSON.stringify(payload), created_at: new Date(Date.parse(startTime) + eventID * 1000).toISOString() });
  }
  append(completed.ref, 21, "message", { content: "Please review the navigation states." });
  append(completed.ref, 21, "thinking_delta", { content: "Compared desktop and mobile constraints." });
  append(completed.ref, 21, "tool_call", { tool_id: "viewport", tool_name: "Read", input: { file_path: "/workspace/project/navigation.ts" } });
  append(completed.ref, 21, "tool_result", { tool_id: "viewport", tool_name: "Read", output: "Viewport checks completed without overflow." });
  append(completed.ref, 21, "session_handoff", { package_id: "fixture-beta-package", package_sha256: "beta123fixture", package: { source: { ref: managed.ref }, stage_summary: "Fixture handoff payload" } });
  append(completed.ref, 21, "completed", { response: "The responsive review is complete.", model: "fixture-model" });
  const conversation = (session: FixtureSession) => ({ schema_version: "niuma.session-conversation.v1", session, events: events.get(session.ref) ?? [], cursor: String(events.get(session.ref)?.at(-1)?.id ?? 0), has_more: false });
  await page.addInitScript(() => {
    const key = "niuma-webui.language.v1";
    if (!localStorage.getItem(key)) localStorage.setItem(key, "en");
  });
  await page.route((url) => /^(?:\/api)?\/(?:tenant|v1|agent|health)(?:\/|$)/.test(url.pathname), async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const rename = /\/tenant\/sessions\/(\d+)$/.exec(url.pathname);
    if (rename && request.method() === "PATCH") {
      const session = sessions.find((item) => item.id === Number(rename[1]) && item.source === "tenant");
      if (!session) return route.fulfill({ status: 404, json: { error: "not_found" } });
      session.title = (request.postDataJSON() as { title: string }).title;
      return route.fulfill({ json: { id: session.id } });
    }
    if (url.pathname.endsWith("/conversations/stream")) {
      const requested = request.postDataJSON() as { sessions?: Array<{ ref: string }> };
      const body = sessions.filter((session) => session.source === "tenant" && requested.sessions?.some((item) => item.ref === session.ref)).map((session) => `event: conversation\ndata: ${JSON.stringify(conversation(session))}\n\n`).join("");
      return route.fulfill({ contentType: "text/event-stream", body });
    }
    if (url.pathname.endsWith("/session-control/sessions")) return route.fulfill({ json: { data: sessions.filter((session) => session.source === url.searchParams.get("source")) } });
    const match = /\/session-control\/sessions\/(tenant|local)\/([^/]+)(?:\/(conversation|messages|stop))?$/.exec(url.pathname);
    if (match) {
      const session = sessions.find((item) => item.ref === `${match[1]}:${decodeURIComponent(match[2])}`);
      if (!session) return route.fulfill({ status: 404, json: { error: { code: "not_found" } } });
      if (match[3] === "conversation") return route.fulfill({ json: { data: conversation(session) } });
      if (match[3] === "stop") {
        session.status = "stopped";
        append(session.ref, session.active_run_id!, "cancelled", {});
      } else if (match[3] === "messages") {
        const input = request.postDataJSON() as { content: string; source_refs?: string[] };
        session.active_run_id = (session.active_run_id ?? 30) + 1;
        session.status = "running";
        append(session.ref, session.active_run_id, "message", { content: input.content });
        for (const ref of input.source_refs ?? []) append(session.ref, session.active_run_id, "session_handoff", { package_id: `handoff-${eventID}`, package_sha256: "sourcecontextverified", package: { source: { ref }, stage_summary: "New context attached" } });
      } else return route.fulfill({ json: { data: session } });
      session.updated_at = new Date(Date.parse(startTime) + eventID * 1000).toISOString();
      return route.fulfill({ json: { data: { operation_id: `operation-${++operationID}`, session, replayed: false } } });
    }
    if (url.pathname.endsWith("/pending-inputs")) return route.fulfill({ json: { data: [] } });
    if (url.pathname.endsWith("/pending-input-settings")) return route.fulfill({ json: { enabled: true } });
    if (url.pathname.endsWith("/v1/providers")) return route.fulfill({ json: { data: [{ name: "openai", model: "fixture-model" }] } });
    if (url.pathname.endsWith("/v1/models")) return route.fulfill({ json: { data: [{ id: "fixture-model" }] } });
    if (url.pathname.endsWith("/health")) return route.fulfill({ json: { ok: true, model: "fixture-model", workspace: managed.cwd } });
    if (url.pathname.includes("/web-agent/conversations/")) {
      const sessionID = Number(decodeURIComponent(url.pathname.split("/").at(-1) ?? "").split(":").at(-1));
      const session = sessions.find((item) => item.id === sessionID) ?? managed;
      const task = { id: session.active_run_id, session_id: session.id, status: session.status, model: session.model, metadata_json: JSON.stringify({ permission_mode: "ask", effort: "high", prompt_mode: "code" }) };
      return route.fulfill({ json: { tasks: [task], latest_task: task, events: [], usage: {} } });
    }
    return route.fulfill({ json: { data: [] } });
  });
}

export async function dropSessionContext(page: Page, ref: string): Promise<void> {
  await page.getByRole("textbox", { name: "Message", exact: true }).evaluate((node, sourceRef) => {
    const dataTransfer = new DataTransfer();
    dataTransfer.setData("application/x-niuma-session-ref", sourceRef);
    node.dispatchEvent(new DragEvent("drop", { bubbles: true, cancelable: true, dataTransfer }));
  }, ref);
}
