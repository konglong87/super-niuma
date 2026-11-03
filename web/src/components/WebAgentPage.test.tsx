import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { buildConversationMessages, StreamingAssistantContent, WebAgentPage } from "./WebAgentPage";
import { I18nProvider } from "../lib/i18n";
import type { IdentityConfig } from "../lib/types";
import { readConversationDraft, writeConversationDraft } from "../lib/conversationDraft";
import {
  cancelAgentTask,
  createPendingInputSideChat,
  createAgentTask,
  createTenantSession,
  getAgentTask,
  getGlobalSettings,
  getPendingInputQueueSettings,
  getWebAgentConversation,
  getStatus,
  listAgentSlashCommands,
  listAgentTaskEvents,
  listAgentTasks,
  listModels,
  listPendingInputs,
  listProviders,
  listTenantSessions,
  listWebAgentConversations,
  presignMobileAttachment,
  resolveAgentTaskPermission,
  sendAgentTaskMessage,
  streamAgentTaskEvents,
  uploadAttachmentBinary,
  validateAgentWorkspace
} from "../lib/api";

vi.mock("../lib/api", () => ({
  cancelAgentTask: vi.fn(),
  createPendingInputSideChat: vi.fn(),
  getAgentTask: vi.fn(async (_identity, taskID) => ({ id: taskID, agent_name: "web-agent", status: "completed" })),
  getGlobalSettings: vi.fn(async () => ({ path: "", exists: false, doc: {}, masked: [] })),
  getPendingInputQueueSettings: vi.fn(async () => ({ enabled: true })),
  createAgentTask: vi.fn(),
  createTenantSession: vi.fn(),
  getStatus: vi.fn(async () => ({
    workspace: "/Users/example/GolandProjects/niuma"
  })),
  getWebAgentConversation: vi.fn(async () => ({ id: "legacy:0", title: "empty", status: "unknown", latest_task: { id: 0 }, tasks: [], events: [], usage: {} })),
  listAgentSlashCommands: vi.fn(async () => []),
  listAgentTaskEvents: vi.fn(async () => []),
  listAgentTasks: vi.fn(async () => []),
  listModels: vi.fn(async () => ["gpt-5.5"]),
  listPendingInputs: vi.fn(async () => []),
  listProviders: vi.fn(async () => []),
  listTenantSessions: vi.fn(async () => []),
  listWebAgentConversations: vi.fn(async () => []),
  presignMobileAttachment: vi.fn(),
  resolveAgentTaskPermission: vi.fn(),
  sendAgentTaskMessage: vi.fn(),
  streamAgentTaskEvents: vi.fn(async (_identity, _taskId, callbacks) => {
    callbacks.onDone();
  }),
  uploadAttachmentBinary: vi.fn(),
  validateAgentWorkspace: vi.fn(async () => ({
    cwd: "/Users/example/GolandProjects/niuma",
    workspace_name: "niuma",
    exists: true,
    is_dir: true,
    is_git_repo: true
  }))
}));

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "test-token",
  mobileJwt: "",
  tenantKey: "webui-local",
  userId: "webui-local-user",
  deviceId: "test-device",
  role: "owner",
  model: "gpt-5.5"
};

describe("WebAgentPage i18n", () => {
  let host: HTMLDivElement;
  let root: Root;
  let originalScrollIntoView: typeof Element.prototype.scrollIntoView;
  let originalScrollTo: typeof Element.prototype.scrollTo;
  const storage = new Map<string, string>();

  beforeEach(() => {
    vi.mocked(cancelAgentTask).mockReset().mockResolvedValue({ id: 41, cancelled: true });
    vi.mocked(createPendingInputSideChat).mockReset().mockResolvedValue({ session_id: 88, task_id: 99, source_pending_input_id: "pi-1" });
    vi.mocked(createAgentTask).mockReset().mockResolvedValue(77);
    vi.mocked(createTenantSession).mockReset().mockResolvedValue(17);
    vi.mocked(getAgentTask).mockReset().mockImplementation(async (_identity, taskID) => ({ id: taskID, agent_name: "web-agent", status: "completed" }));
    vi.mocked(getGlobalSettings).mockReset().mockResolvedValue({ path: "", exists: false, doc: {}, masked: [] });
    vi.mocked(getPendingInputQueueSettings).mockReset().mockResolvedValue({ enabled: true });
    vi.mocked(getStatus).mockReset().mockResolvedValue({
      workspace: "/Users/example/GolandProjects/niuma"
    });
    vi.mocked(getWebAgentConversation).mockReset().mockResolvedValue({ id: "legacy:0", title: "empty", status: "unknown", latest_task: { id: 0 }, tasks: [], events: [], usage: {} });
    vi.mocked(listAgentSlashCommands).mockReset().mockResolvedValue([]);
    vi.mocked(listAgentTaskEvents).mockReset().mockResolvedValue([]);
    vi.mocked(listAgentTasks).mockReset().mockResolvedValue([]);
    vi.mocked(listModels).mockReset().mockResolvedValue(["gpt-5.5"]);
    vi.mocked(listPendingInputs).mockReset().mockResolvedValue([]);
    vi.mocked(listProviders).mockReset().mockResolvedValue([]);
    vi.mocked(listTenantSessions).mockReset().mockResolvedValue([]);
    vi.mocked(listWebAgentConversations).mockReset().mockResolvedValue([]);
    vi.mocked(presignMobileAttachment).mockReset().mockRejectedValue(new Error("mobile upload unavailable"));
    vi.mocked(resolveAgentTaskPermission).mockReset().mockResolvedValue({ id: 1, request_id: "perm-1", allowed: true });
    vi.mocked(sendAgentTaskMessage).mockReset().mockResolvedValue(99);
    vi.mocked(uploadAttachmentBinary).mockReset().mockResolvedValue();
    vi.mocked(streamAgentTaskEvents).mockReset().mockImplementation(async (_identity, _taskId, callbacks) => {
      callbacks.onDone();
    });
    vi.mocked(validateAgentWorkspace).mockReset().mockResolvedValue({
      cwd: "/Users/example/GolandProjects/niuma",
      workspace_name: "niuma",
      exists: true,
      is_dir: true,
      is_git_repo: true
    });
    originalScrollIntoView = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = originalScrollIntoView || vi.fn();
    originalScrollTo = Element.prototype.scrollTo;
    Element.prototype.scrollTo = originalScrollTo || vi.fn();
    storage.clear();
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        getItem: (key: string) => storage.get(key) || null,
        setItem: (key: string, value: string) => storage.set(key, value),
        removeItem: (key: string) => storage.delete(key),
        clear: () => storage.clear()
      }
    });
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => {
      root.unmount();
    });
    Element.prototype.scrollIntoView = originalScrollIntoView;
    Element.prototype.scrollTo = originalScrollTo;
  });

  it("renders the standalone Web Agent surface in Chinese when language is zh", async () => {
    window.localStorage.setItem("niuma-webui.language.v1", "zh");

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    expect(host.textContent).toContain("新会话");
    expect(host.textContent).toContain("工作区");
    expect(host.textContent).toContain("切换");
    expect(host.textContent).toContain("权限");
    expect(host.textContent).toContain("当前租户还没有 Web Agent 会话。");
    expect(host.querySelector('[aria-label="语言"]')).not.toBeNull();
    expect(host.querySelector(".agent-language-switch button.active")?.textContent).toBe("中文");
  });

  it("opens a pending input side chat as the selected session", async () => {
    window.localStorage.setItem("niuma-webui.language.v1", "zh");
    let sideChatCreated = false;
    vi.mocked(createPendingInputSideChat).mockImplementation(async () => {
      sideChatCreated = true;
      return { session_id: 88, task_id: 99, source_pending_input_id: "pi-1" };
    });
    vi.mocked(listPendingInputs).mockResolvedValue([{ id: "pi-1", session_id: "5", client_input_id: "side", content: "侧聊候选", sequence: 1, status: "queued" }]);
    vi.mocked(listAgentTasks).mockImplementation(async () => sideChatCreated ? [
      { id: 41, parent_session_id: 5, agent_name: "web-agent", description: "Main", status: "running", metadata_json: "{\"cwd\":\"/repo\"}" },
      { id: 99, parent_session_id: 88, agent_name: "web-agent", description: "Pending input side chat", status: "ready", metadata_json: "{\"cwd\":\"/repo\",\"source_pending_input_id\":\"pi-1\"}" }
    ] : [
      { id: 41, parent_session_id: 5, agent_name: "web-agent", description: "Main", status: "running", metadata_json: "{\"cwd\":\"/repo\"}" }
    ]);

    await act(async () => {
      root.render(<I18nProvider><WebAgentPage identity={identity} onStatus={vi.fn()} /></I18nProvider>);
    });
    const more = await vi.waitFor(() => {
      const button = host.querySelector<HTMLButtonElement>('button[aria-label="更多等待输入操作"]');
      expect(button).not.toBeNull();
      return button as HTMLButtonElement;
    });
    await act(async () => more.click());
    const openSideChat = Array.from(host.querySelectorAll("[role=menuitem]")).find((node) => node.textContent?.includes("在侧边聊天中打开")) as HTMLButtonElement;
    await act(async () => openSideChat.click());

    await vi.waitFor(() => expect(createPendingInputSideChat).toHaveBeenCalledWith(identity, 41, "pi-1"));
    await vi.waitFor(() => expect(host.querySelector(".agent-run-row.active")?.textContent).toContain("task 99"));
  });

  it("leaves queued input consumption to the server coordinator", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 41, parent_session_id: 5, agent_name: "web-agent", description: "Completed", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listPendingInputs).mockResolvedValue([{ id: "pi-1", session_id: "5", client_input_id: "server-owned", content: "server drains this", sequence: 1, status: "queued" }]);

    await act(async () => {
      root.render(<I18nProvider><WebAgentPage identity={identity} onStatus={vi.fn()} /></I18nProvider>);
    });
    await vi.waitFor(() => expect(host.textContent).toContain("server drains this"));
    await new Promise((resolve) => window.setTimeout(resolve, 50));
    expect(createAgentTask).not.toHaveBeenCalled();
    expect(sendAgentTaskMessage).not.toHaveBeenCalled();
  });

  it("does not leave image tool activity running when image_artifact precedes tool_result", () => {
    const task = { id: 61, status: "completed", model: "gpt-5.6-sol", metadata_json: "{}", result_json: JSON.stringify({ response: "图片已生成完毕" }) } as never;
    const event = (id: number, event_type: string, payload_json: string) => ({ id, task_id: 61, event_type, payload_json, created_at: `2026-09-02T07:39:${String(id).padStart(2, "0")}.000Z` });
    const messages = buildConversationMessages(task, [
      event(1, "text_delta", JSON.stringify({ content: "我将开始生成" })),
      event(2, "tool_call", JSON.stringify({ tool_name: "GenerateImage", tool_id: "tool-1", input: "{}" })),
      event(3, "image_artifact", JSON.stringify({ asset_id: "asset-1", url: "/tenant/media/assets/asset-1", media_type: "image/png" })),
      event(4, "tool_result", JSON.stringify({ tool_name: "GenerateImage", tool_id: "tool-1", output: JSON.stringify({ asset_id: "asset-1" }), is_error: false })),
      event(5, "text_delta", JSON.stringify({ content: "图片已生成完毕" }))
    ], []);
    expect(messages.flatMap((message) => message.tools || []).some((tool) => tool.status === "running")).toBe(false);
  });

  it("projects shared runtime questions for the legacy Web Agent conversation", () => {
    const task = { id: 62, status: "running", model: "gpt-5.6-sol", metadata_json: "{}" } as never;
    const event = (id: number, event_type: string, payload: object) => ({ id, task_id: 62, event_type, payload_json: JSON.stringify(payload), created_at: `2026-09-07T00:00:0${id}.000Z` });
    const pending = buildConversationMessages(task, [event(1, "user_question_request", { request_id: "question-62", question: "Which channel?", choices: ["Stable", "Canary"] })]);
    expect(pending[0]).toMatchObject({ kind: "question", content: "Which channel?", taskID: 62 });
    expect((pending[0] as unknown as { question: unknown }).question).toMatchObject({ requestID: "question-62", choices: ["Stable", "Canary"], status: "pending" });

    const answered = buildConversationMessages(task, [
      event(1, "user_question_request", { request_id: "question-62", question: "Which channel?", choices: ["Stable", "Canary"] }),
      event(2, "user_question_resolved", { request_id: "question-62", status: "answered", answer: "Canary" })
    ]);
    expect((answered[0] as unknown as { question: unknown }).question).toMatchObject({ status: "answered", answer: "Canary" });
  });

  it("does not duplicate a runtime question from its explicitly linked failed tool", () => {
    const task = { id: 62, status: "running", model: "gpt-5.6-sol", metadata_json: "{}" } as never;
    const event = (id: number, event_type: string, payload: object) => ({ id, task_id: 62, event_type, payload_json: JSON.stringify(payload), created_at: `2026-09-07T00:00:0${id}.000Z` });
    const messages = buildConversationMessages(task, [
      event(1, "tool_call", { tool_id: "ask-62", tool_name: "AskUserQuestion", input: { question: "Continue?", choices: ["Yes", "No"] } }),
      event(2, "user_question_request", { request_id: "question-62", tool_id: "ask-62", question: "Continue?", choices: ["Yes", "No"] }),
      event(3, "user_question_resolved", { request_id: "question-62", status: "expired" }),
      event(4, "tool_result", { tool_id: "ask-62", tool_name: "AskUserQuestion", is_error: true, error: "question expired" })
    ]);

    expect(messages.filter((message) => message.kind === "question")).toHaveLength(1);
    expect(messages.find((message) => message.kind === "question")?.question).toMatchObject({ status: "expired" });
    expect(messages.find((message) => message.kind === "question")?.question?.legacy).toBeUndefined();
  });

  it("associates an old runtime question event with its active tool by prompt", () => {
    const task = { id: 62, status: "running", model: "gpt-5.6-sol", metadata_json: "{}" } as never;
    const event = (id: number, event_type: string, payload: object) => ({ id, task_id: 62, event_type, payload_json: JSON.stringify(payload), created_at: `2026-09-07T00:00:0${id}.000Z` });
    const messages = buildConversationMessages(task, [
      event(1, "tool_call", { tool_id: "ask-old", tool_name: "AskUserQuestion", input: { question: "Continue?", choices: ["Yes"] } }),
      event(2, "user_question_request", { request_id: "question-old", question: "Continue?", choices: ["Yes"] }),
      event(3, "user_question_resolved", { request_id: "question-old", status: "cancelled" }),
      event(4, "tool_result", { tool_id: "ask-old", tool_name: "AskUserQuestion", is_error: true, error: "question cancelled" })
    ]);

    expect(messages.filter((message) => message.kind === "question")).toHaveLength(1);
    expect(messages.find((message) => message.kind === "question")?.question).toMatchObject({ status: "cancelled" });
    expect(messages.find((message) => message.kind === "question")?.question?.legacy).toBeUndefined();
  });

  it("keeps a legacy failed AskUserQuestion record readable but non-actionable", () => {
    const task = { id: 63, status: "completed", model: "gpt-5.6-sol", metadata_json: "{}" } as never;
    const event = (id: number, event_type: string, payload: object) => ({ id, task_id: 63, event_type, payload_json: JSON.stringify(payload), created_at: `2026-09-07T00:00:0${id}.000Z` });
    const messages = buildConversationMessages(task, [
      event(1, "tool_call", { tool_id: "ask", tool_name: "AskUserQuestion", input: JSON.stringify({ questions: [{ question: "Pick storage", options: [{ label: "S3" }, { label: "Local" }] }] }) }),
      event(2, "tool_result", { tool_id: "ask", tool_name: "AskUserQuestion", is_error: true, error: "callback unavailable" })
    ]);
    expect(messages[0]).toMatchObject({ kind: "question", content: "Pick storage" });
    expect((messages[0] as unknown as { question: unknown }).question).toMatchObject({ choices: ["S3", "Local"], status: "unavailable", legacy: true });
  });

  it("preserves native choices from a historical AskUserQuestion", () => {
    const task = { id: 62, status: "completed" };
    const event = (id: number, type: string, payload: unknown) => ({ id, task_id: 62, event_type: type, payload_json: JSON.stringify(payload) });
    const messages = buildConversationMessages(task, [
      event(1, "tool_call", { tool_id: "ask", tool_name: "AskUserQuestion", input: { question: "Diagram?", choices: ["Architecture", "Sequence"] } }),
      event(2, "tool_result", { tool_id: "ask", tool_name: "AskUserQuestion", is_error: true })
    ]);
    expect(messages[0].question?.choices).toEqual(["Architecture", "Sequence"]);
  });


  it("restores and autosaves the Web Agent composer draft per session", async () => {
    writeConversationDraft({ surface: "web-agent", sessionKey: "legacy:77", workspace: "/Users/example/GolandProjects/niuma", text: "resume this draft", updatedAt: "2026-08-31T00:00:00Z" });
    vi.mocked(listAgentTasks).mockResolvedValue([{
      id: 77,
      agent_name: "web-agent",
      description: "Draft session",
      status: "completed",
      metadata_json: "{\"cwd\":\"/repo\"}",
      result_json: "{\"response\":\"answer\"}"
    }]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const composer = await vi.waitFor(() => {
      const node = host.querySelector<HTMLTextAreaElement>('textarea[aria-label="Message composer"]');
      expect(node?.value).toBe("resume this draft");
      return node as HTMLTextAreaElement;
    });
    await act(async () => {
      const setValue = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set;
      setValue?.call(composer, "updated draft");
      composer.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await new Promise((resolve) => window.setTimeout(resolve, 450));
    expect(readConversationDraft("web-agent", "legacy:77", "/Users/example/GolandProjects/niuma")?.text).toBe("updated draft");
  });

  it("recovers the local Web Agent identity after a cached tenant-not-found error", async () => {
    window.history.replaceState({}, "", "/webui/agent?token=test-token");
    const staleIdentity = { ...identity, tenantKey: "ai-study", userId: "ai-study-user" };
    vi.mocked(listAgentTasks).mockImplementation(async (currentIdentity) => {
      if (currentIdentity.tenantKey === "ai-study") {
        throw new Error("get tenant ai-study: mysql storage: not found");
      }
      return [];
    });

    function IdentityHarness() {
      const [currentIdentity, setCurrentIdentity] = useState(staleIdentity);
      return <WebAgentPage identity={currentIdentity} onIdentityChange={setCurrentIdentity} onStatus={vi.fn()} />;
    }

    await act(async () => {
      root.render(<I18nProvider><IdentityHarness /></I18nProvider>);
    });
    await vi.waitFor(() => expect(host.textContent).toContain("get tenant ai-study: mysql storage: not found"));
    const recoverButton = await vi.waitFor(() => {
      const button = Array.from(host.querySelectorAll("button")).find((item) => item.textContent?.includes("Restore Web Agent test identity"));
      expect(button).toBeDefined();
      return button as HTMLButtonElement;
    });

    await act(async () => recoverButton.click());

    await vi.waitFor(() => expect(listAgentTasks).toHaveBeenCalledWith(expect.objectContaining({ tenantKey: "webui-local", userId: "webui-local-user" }), expect.anything()));
    const persisted = JSON.parse(storage.get("niuma-webui.identity.v1") || "{}");
    expect(persisted).toMatchObject({ apiBase: "/api", apiToken: "test-token", tenantKey: "webui-local", userId: "webui-local-user" });
    await vi.waitFor(() => expect(host.textContent).not.toContain("get tenant ai-study"));
  });

  it("keeps the runtime top bar model in sync with the composer model picker", async () => {
    vi.mocked(listModels).mockResolvedValue(["glm-5.1", "deepseek-v4-pro"]);
    vi.mocked(listAgentTasks).mockResolvedValue([
      { id: 71, agent_name: "web-agent", description: "Picker session", status: "ready", model: "glm-5.1", metadata_json: "{\"cwd\":\"/Users/example/GolandProjects/niuma\",\"workspace_name\":\"niuma\"}" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.textContent).toContain("Picker session"));
    await act(async () => {
      (host.querySelector(".session-row") as HTMLButtonElement).click();
    });
    await vi.waitFor(() => expect(host.querySelector(".agent-runtime-topbar")?.textContent).toContain("glm-5.1"));

    await act(async () => {
      (host.querySelector(".composer-meta-control.model .agent-popover-trigger") as HTMLButtonElement).click();
    });
    const option = Array.from(host.querySelectorAll(".agent-popover-option")).find((node) => node.textContent?.includes("deepseek-v4-pro")) as HTMLElement;
    await act(async () => {
      option.click();
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-runtime-topbar")?.textContent).toContain("deepseek-v4-pro"));
  });

  it("fills the composer when a blank-session suggestion is clicked", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([
      { id: 81, agent_name: "web-agent", description: "Blank session", status: "ready", model: "glm-5.1", metadata_json: "{\"cwd\":\"/Users/example/GolandProjects/niuma\"}" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.textContent).toContain("Blank session"));
    await act(async () => {
      (host.querySelector(".session-row") as HTMLButtonElement).click();
    });
    await vi.waitFor(() => expect(host.querySelector(".agent-blank-suggestion")).not.toBeNull());

    const suggestion = host.querySelector(".agent-blank-suggestion") as HTMLButtonElement;
    const suggestionText = (suggestion.textContent || "").trim();
    await act(async () => {
      suggestion.click();
    });

    const composer = host.querySelector('textarea[aria-label="Message composer"]') as HTMLTextAreaElement;
    expect(composer.value).toBe(suggestionText);
  });

  it("renders a readable activity timeline with collapsed reply deltas", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([
      { id: 90, agent_name: "web-agent", description: "Collapse events", status: "completed", model: "glm-5.1", metadata_json: "{\"cwd\":\"/repo\"}" }
    ]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 90, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"q\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 90, event_type: "tool_call", payload_json: "{\"tool_id\":\"t1\",\"tool_name\":\"Read\"}", created_at: "2026-06-30T00:00:01Z" },
      { id: 3, task_id: 90, event_type: "tool_result", payload_json: "{\"tool_id\":\"t1\",\"tool_name\":\"Read\"}", created_at: "2026-06-30T00:00:02Z" },
      { id: 4, task_id: 90, event_type: "text_delta", payload_json: "{\"content\":\"a\"}", created_at: "2026-06-30T00:00:03Z" },
      { id: 5, task_id: 90, event_type: "text_delta", payload_json: "{\"content\":\"b\"}", created_at: "2026-06-30T00:00:04Z" },
      { id: 6, task_id: 90, event_type: "text_delta", payload_json: "{\"content\":\"c\"}", created_at: "2026-06-30T00:00:05Z" },
      { id: 7, task_id: 90, event_type: "usage", payload_json: "{}", created_at: "2026-06-30T00:00:06Z" },
      { id: 8, task_id: 90, event_type: "completed", payload_json: "{}", created_at: "2026-06-30T00:00:07Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.textContent).toContain("Collapse events"));
    await act(async () => {
      host.querySelector<HTMLButtonElement>("[title='Toggle right panel']")?.click();
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-timeline")).not.toBeNull());
    const timelineText = host.querySelector(".agent-timeline")?.textContent || "";
    expect(timelineText).toContain("Prompt sent");
    expect(timelineText).toContain("Read");
    expect(timelineText).toContain("Streaming reply");
    expect(timelineText).toContain("×3");
    expect(timelineText).toContain("Completed");
    expect(timelineText).not.toContain("usage");
    const replyRows = Array.from(host.querySelectorAll(".agent-timeline-row")).filter((node) => node.textContent?.includes("Streaming reply"));
    expect(replyRows).toHaveLength(1);

    // 工具步骤:显示耗时,可展开查看 payload
    const toolItem = host.querySelector(".agent-timeline-item");
    expect(toolItem).not.toBeNull();
    expect(toolItem?.textContent).toContain("done · 1s");
    expect(toolItem?.querySelector(".agent-timeline-payload")?.textContent).toContain("t1");
  });

  it("renders code fences with a language header and copy button", async () => {
    const writeText = vi.fn(async () => {});
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    vi.mocked(listAgentTasks).mockResolvedValue([
      { id: 95, agent_name: "web-agent", description: "Code fence", status: "completed", model: "glm-5.1", metadata_json: "{\"cwd\":\"/repo\"}" }
    ]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 95, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"q\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 95, event_type: "text_delta", payload_json: "{\"content\":\"```go\\nfmt.Println(1)\\n```\"}", created_at: "2026-06-30T00:00:01Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-codeblock")).not.toBeNull());
    expect(host.querySelector(".agent-codeblock-head")?.textContent).toContain("go");
    expect(host.querySelector(".agent-markdown pre code")?.textContent).toContain("fmt.Println(1)");

    await act(async () => {
      (host.querySelector(".agent-codeblock-copy") as HTMLButtonElement).click();
    });
    expect(writeText).toHaveBeenCalledWith("fmt.Println(1)");
  });

  it("toggles dark theme and persists the choice", async () => {
    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const main = host.querySelector(".web-agent-page");
    expect(main?.getAttribute("data-agent-theme")).toBe("light");

    await act(async () => {
      (host.querySelector('[title="Toggle theme"]') as HTMLButtonElement).click();
    });
    expect(main?.getAttribute("data-agent-theme")).toBe("dark");
    expect(window.localStorage.getItem("niuma-webui.agent.theme.v1")).toBe("dark");
  });

  it("windows long conversations and reveals earlier messages on demand", async () => {
    const events = Array.from({ length: 150 }, (_, index) => ({
      id: index + 1,
      task_id: 97,
      event_type: "message",
      payload_json: `{"from_agent":"webui","content":"msg-${index + 1}"}`,
      created_at: `2026-06-30T00:${String(Math.floor(index / 60)).padStart(2, "0")}:${String(index % 60).padStart(2, "0")}Z`
    }));
    vi.mocked(listAgentTasks).mockResolvedValue([
      { id: 97, agent_name: "web-agent", description: "Long session", status: "completed", model: "glm-5.1", metadata_json: "{\"cwd\":\"/repo\"}" }
    ]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue(events);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelectorAll(".agent-message").length).toBeGreaterThan(0));
    expect(host.querySelectorAll(".agent-message")).toHaveLength(80);
    expect(host.textContent).toContain("Show 70 earlier messages");
    expect(host.textContent).not.toContain("msg-1 ");

    await act(async () => {
      (host.querySelector(".agent-show-earlier") as HTMLButtonElement).click();
    });
    await vi.waitFor(() => expect(host.querySelectorAll(".agent-message")).toHaveLength(150));
    expect(host.querySelector(".agent-show-earlier")).toBeNull();
  });

  it("shows reply duration on assistant messages", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([
      { id: 98, agent_name: "web-agent", description: "Timed reply", status: "completed", model: "glm-5.1", metadata_json: "{\"cwd\":\"/repo\"}" }
    ]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 98, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"hi\"}", created_at: "2026-06-30T10:00:00Z" },
      { id: 2, task_id: 98, event_type: "text_delta", payload_json: "{\"content\":\"hello \"}", created_at: "2026-06-30T10:00:05Z" },
      { id: 3, task_id: 98, event_type: "text_delta", payload_json: "{\"content\":\"there\"}", created_at: "2026-06-30T10:00:12Z" },
      { id: 4, task_id: 98, event_type: "completed", payload_json: "{}", created_at: "2026-06-30T10:00:19Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-message.assistant")).not.toBeNull());
    const duration = host.querySelector(".agent-message.assistant .agent-message-duration");
    expect(duration).not.toBeNull();
    expect(duration?.textContent).toContain("19s");
    const modelTag = host.querySelector(".agent-message.assistant .agent-message-model");
    expect(modelTag?.textContent).toContain("glm-5.1");
  });

  it("aligns footer model to the named provider's own model", async () => {
    vi.mocked(listProviders).mockResolvedValue([
      { name: "sensenova-deepseek-v4-flash", model: "deepseek-v4-flash" },
      { name: "glm-5.1", model: "glm-5.1" }
    ]);
    // Stored data is inconsistent (provider routes to deepseek-v4-flash but task.model
    // still says glm-5.1). The footer must show the provider's own model, not task.model.
    vi.mocked(listAgentTasks).mockResolvedValue([
      { id: 77, agent_name: "web-agent", description: "Crossed", status: "completed", model: "glm-5.1", metadata_json: "{\"cwd\":\"/repo\",\"provider\":\"sensenova-deepseek-v4-flash\"}" }
    ]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 77, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"hi\"}", created_at: "2026-06-30T10:00:00Z" },
      { id: 2, task_id: 77, event_type: "text_delta", payload_json: "{\"content\":\"hello\"}", created_at: "2026-06-30T10:00:05Z" },
      { id: 3, task_id: 77, event_type: "completed", payload_json: "{}", created_at: "2026-06-30T10:00:08Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const modelTag = await vi.waitFor(() => {
      const node = host.querySelector(".agent-message.assistant .agent-message-model");
      expect(node).not.toBeNull();
      return node!;
    });
    expect(modelTag.textContent).toContain("sensenova-deepseek-v4-flash");
    expect(modelTag.textContent).toContain("deepseek-v4-flash");
    expect(modelTag.textContent).not.toContain("glm-5.1");
  });

  it("keeps each continuation reply bound to its own persisted provider", () => {
    const providers = [
      { name: "provider-a", model: "model-a" },
      { name: "provider-b", model: "model-b" }
    ];
    const firstTask = {
      id: 101,
      agent_name: "web-agent",
      status: "completed",
      model: "model-a",
      metadata_json: JSON.stringify({ provider: "provider-a" })
    } as never;
    const continuationTask = {
      id: 102,
      agent_name: "web-agent",
      status: "completed",
      model: "model-b",
      metadata_json: JSON.stringify({ provider: "provider-b", continuation_of_task_id: 101 })
    } as never;
    const event = (id: number, task_id: number, event_type: string, payload: Record<string, unknown>) => ({
      id,
      task_id,
      event_type,
      payload_json: JSON.stringify(payload),
      created_at: `2026-06-30T10:00:${String(id).padStart(2, "0")}Z`
    });
    const messages = buildConversationMessages(
      continuationTask,
      [
        event(1, 101, "message", { from_agent: "webui", content: "first question" }),
        event(2, 101, "text_delta", { content: "reply from A" }),
        event(3, 101, "completed", {}),
        event(4, 102, "message", { from_agent: "webui", content: "follow-up question" }),
        event(5, 102, "text_delta", { content: "reply from B" }),
        event(6, 102, "completed", {})
      ],
      providers,
      [firstTask, continuationTask]
    );

    const assistantMessages = messages.filter((message) => message.role === "assistant" && message.content.trim());
    expect(assistantMessages).toHaveLength(2);
    expect(assistantMessages.map((message) => message.provider)).toEqual(["provider-a", "provider-b"]);
    expect(assistantMessages.map((message) => message.model)).toEqual(["model-a", "model-b"]);
  });

  it("recovers newlines from completed result when streamed deltas dropped them", async () => {
    // delta 拼接丢了换行(标题与表头/表格行全挤在一起),但 completed 的
    // result.response 换行齐全 —— 完成态应采信 response,把表格正确渲染出来。
    const brokenDelta = "## 可行性评估| 模块 | 难度 ||------|------|| 2.1 检查 | ⭐⭐⭐ |";
    const fullResponse = "## 可行性评估\n\n| 模块 | 难度 |\n|------|------|\n| 2.1 检查 | ⭐⭐⭐ |";
    vi.mocked(listAgentTasks).mockResolvedValue([
      { id: 88, agent_name: "web-agent", description: "Table recover", status: "completed", model: "glm-5.1", metadata_json: "{\"cwd\":\"/repo\"}", result_json: JSON.stringify({ response: fullResponse }) }
    ]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 88, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"q\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 88, event_type: "text_delta", payload_json: JSON.stringify({ content: brokenDelta }), created_at: "2026-06-30T00:00:01Z" },
      { id: 3, task_id: 88, event_type: "completed", payload_json: "{}", created_at: "2026-06-30T00:00:02Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-markdown table")).not.toBeNull());
    const headers = Array.from(host.querySelectorAll(".agent-markdown th")).map((node) => node.textContent);
    expect(headers).toEqual(["模块", "难度"]);
    expect(host.querySelector(".agent-markdown")?.textContent).not.toContain("|------|");
  });

  it("shows per-reply token usage in the message footer", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([
      { id: 91, agent_name: "web-agent", description: "Token reply", status: "completed", model: "glm-5.1", metadata_json: "{\"cwd\":\"/repo\"}" }
    ]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 91, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"hi\"}", created_at: "2026-06-30T10:00:00Z" },
      { id: 2, task_id: 91, event_type: "text_delta", payload_json: "{\"content\":\"hello\"}", created_at: "2026-06-30T10:00:01Z" },
      { id: 3, task_id: 91, event_type: "usage", payload_json: "{\"input_tokens\":320,\"output_tokens\":890,\"total_tokens\":1210}", created_at: "2026-06-30T10:00:02Z" },
      { id: 4, task_id: 91, event_type: "completed", payload_json: "{}", created_at: "2026-06-30T10:00:03Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-message.assistant .agent-message-tokens")).not.toBeNull());
    const tokens = host.querySelector(".agent-message.assistant .agent-message-tokens");
    expect(tokens?.textContent).toContain("1.2k tokens");
    expect(tokens?.getAttribute("title")).toContain("1,210");
  });

  it("renders markdown tables as real tables", async () => {
    const markdown = "最近提交如下：\\n\\n| 提交 | 说明 |\\n|------|------|\\n| `d914b62` | fix: 模块计数修正 |\\n| `5cad1b5` | fix: 幽灵引用 |";
    vi.mocked(listAgentTasks).mockResolvedValue([
      { id: 96, agent_name: "web-agent", description: "Table reply", status: "completed", model: "glm-5.1", metadata_json: "{\"cwd\":\"/repo\"}" }
    ]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 96, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"q\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 96, event_type: "text_delta", payload_json: `{"content":"${markdown}"}`, created_at: "2026-06-30T00:00:01Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-markdown table")).not.toBeNull());
    const headers = Array.from(host.querySelectorAll(".agent-markdown th")).map((node) => node.textContent);
    expect(headers).toEqual(["提交", "说明"]);
    expect(host.querySelectorAll(".agent-markdown tbody tr")).toHaveLength(2);
    expect(host.querySelector(".agent-markdown tbody td code")?.textContent).toBe("d914b62");
    expect(host.querySelector(".agent-markdown")?.textContent).not.toContain("|------|");
  });


  it("renders one session navigation grouped by workspace with search", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([
      { id: 61, agent_name: "web-agent", description: "Current workspace task", status: "ready", metadata_json: "{\"cwd\":\"/Users/example/GolandProjects/niuma\",\"workspace_name\":\"niuma\"}" },
      { id: 62, agent_name: "web-agent", description: "Other workspace task", status: "completed", metadata_json: "{\"cwd\":\"/repo/other\",\"workspace_name\":\"other\"}" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.textContent).toContain("Current workspace task"));
    expect(host.querySelector(".workspace-switcher")).toBeNull();
    expect(host.querySelector(".workspace-filter")).not.toBeNull();
    expect(host.querySelector(".workspace-list .agent-section-title")?.textContent).toContain("Sessions");
    expect(host.querySelectorAll(".workspace-group")).toHaveLength(2);
    expect(host.querySelector(".session-row")?.textContent).toContain("Current workspace task");

    const search = host.querySelector<HTMLInputElement>(".workspace-filter");
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")?.set;
      setter?.call(search!, "other");
      search!.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await vi.waitFor(() => expect(host.querySelectorAll(".workspace-group")).toHaveLength(1));
    expect(host.querySelector(".workspace-row-main")?.textContent).toContain("other");
    await act(async () => {
      host.querySelector<HTMLButtonElement>(".workspace-expander")?.click();
    });
    expect(host.querySelector(".session-row")?.textContent).toContain("Other workspace task");
  });

  it("renders a continuation task chain as one session row", async () => {
    vi.mocked(getStatus).mockResolvedValueOnce({
      workspace: "/Users/example/GolandProjects/huyu"
    });
    vi.mocked(listAgentTasks).mockResolvedValue([
      { id: 13, agent_name: "web-agent", description: "yu001", status: "completed", trace_id: "trace-yu", started_at: "2026-07-01T04:23:24Z", finished_at: "2026-07-01T04:23:33Z", metadata_json: "{\"cwd\":\"/Users/example/GolandProjects/huyu\",\"workspace_name\":\"huyu\"}" },
      { id: 14, agent_name: "web-agent", description: "yu001", status: "completed", trace_id: "trace-yu", started_at: "2026-07-01T04:23:40Z", finished_at: "2026-07-01T04:23:46Z", metadata_json: "{\"cwd\":\"/Users/example/GolandProjects/huyu\",\"workspace_name\":\"huyu\",\"continuation_of_task_id\":13}" },
      { id: 15, agent_name: "web-agent", description: "yu001", status: "completed", trace_id: "trace-yu", started_at: "2026-07-01T04:25:58Z", finished_at: "2026-07-01T04:27:22Z", metadata_json: "{\"cwd\":\"/Users/example/GolandProjects/huyu\",\"workspace_name\":\"huyu\",\"continuation_of_task_id\":14}" },
      { id: 16, agent_name: "web-agent", description: "yu001", status: "completed", trace_id: "trace-yu", started_at: "2026-07-01T04:29:39Z", finished_at: "2026-07-01T04:29:49Z", metadata_json: "{\"cwd\":\"/Users/example/GolandProjects/huyu\",\"workspace_name\":\"huyu\",\"continuation_of_task_id\":15}" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.textContent).toContain("huyu"));
    expect(host.querySelector(".workspace-row-main")?.textContent).toContain("1 sessions");
    expect(host.querySelectorAll(".session-row")).toHaveLength(1);
    expect(host.querySelector(".session-row")?.textContent).toContain("yu001");
  });

  it("renders backend web agent conversations at session level", async () => {
    vi.mocked(listWebAgentConversations).mockResolvedValue([
      {
        id: "session:23",
        session_id: 23,
        session_key: "web-agent-session",
        title: "yu001",
        cwd: "/Users/example/GolandProjects/niuma",
        workspace_name: "niuma",
        status: "completed",
        updated_at: "2026-07-01T04:29:49Z",
        session: { id: 23, session_key: "web-agent-session", title: "yu001", status: "active", cwd: "/Users/example/GolandProjects/niuma" },
        latest_task: { id: 16, parent_session_id: 23, agent_name: "web-agent", description: "yu001", status: "completed", started_at: "2026-07-01T04:29:39Z", metadata_json: "{\"cwd\":\"/Users/example/GolandProjects/niuma\",\"prompt_mode\":\"chat\"}" },
        tasks: [
          { id: 13, parent_session_id: 23, agent_name: "web-agent", description: "yu001", status: "completed", started_at: "2026-07-01T04:23:24Z", metadata_json: "{\"cwd\":\"/Users/example/GolandProjects/niuma\"}" },
          { id: 14, parent_session_id: 23, agent_name: "web-agent", description: "yu001", status: "completed", started_at: "2026-07-01T04:23:40Z", metadata_json: "{\"cwd\":\"/Users/example/GolandProjects/niuma\",\"continuation_of_task_id\":13}" },
          { id: 15, parent_session_id: 23, agent_name: "web-agent", description: "yu001", status: "completed", started_at: "2026-07-01T04:25:58Z", metadata_json: "{\"cwd\":\"/Users/example/GolandProjects/niuma\",\"continuation_of_task_id\":14}" },
          { id: 16, parent_session_id: 23, agent_name: "web-agent", description: "yu001", status: "completed", started_at: "2026-07-01T04:29:39Z", metadata_json: "{\"cwd\":\"/Users/example/GolandProjects/niuma\",\"continuation_of_task_id\":15}" }
        ]
      }
    ]);
    vi.mocked(getWebAgentConversation).mockResolvedValue({
      id: "session:23",
      session_id: 23,
      session_key: "web-agent-session",
      title: "yu001",
      cwd: "/Users/example/GolandProjects/niuma",
      workspace_name: "niuma",
      status: "completed",
      updated_at: "2026-07-01T04:29:49Z",
      session: { id: 23, session_key: "web-agent-session", title: "yu001", status: "active", cwd: "/Users/example/GolandProjects/niuma" },
      latest_task: { id: 16, parent_session_id: 23, agent_name: "web-agent", description: "yu001", status: "completed", started_at: "2026-07-01T04:29:39Z", metadata_json: "{\"cwd\":\"/Users/example/GolandProjects/niuma\",\"prompt_mode\":\"chat\"}" },
      tasks: [
        { id: 13, parent_session_id: 23, agent_name: "web-agent", description: "yu001", status: "completed", started_at: "2026-07-01T04:23:24Z", metadata_json: "{\"cwd\":\"/Users/example/GolandProjects/niuma\"}" }
      ],
      events: [
        { id: 1, task_id: 13, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"hello\"}", trace_id: "trace-a", created_at: "2026-07-01T04:23:24Z" },
        { id: 2, task_id: 16, event_type: "completed", payload_json: "{\"response\":\"done\",\"total_tokens\":42,\"context_percent\":3}", trace_id: "trace-b", created_at: "2026-07-01T04:29:49Z" }
      ],
      usage: { total_runs: 2, completed_runs: 2, total_tokens: 42, context_percent: 3, total_duration_ms: 6000 }
    });

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".session-row")?.textContent).toContain("yu001"));
    expect(listWebAgentConversations).toHaveBeenCalled();
    expect(listAgentTasks).not.toHaveBeenCalled();
    expect(host.querySelector(".workspace-row-main")?.textContent).toContain("1 sessions");
    expect(host.querySelectorAll(".session-row")).toHaveLength(1);
    expect(getWebAgentConversation).toHaveBeenCalledWith(identity, "session:23", { limit: 100, eventLimit: 500 });
    await act(async () => {
      host.querySelector<HTMLButtonElement>("[title='Toggle right panel']")?.click();
    });
    const tabs = Array.from(host.querySelectorAll(".right-tabs button")).map((node) => node.textContent || "");
    expect(tabs).toEqual(expect.arrayContaining(["Runs", "Trace", "Usage"]));
    await vi.waitFor(() => expect(host.textContent).toContain("ModeChat"));
  });

  it("returns composer to ready after cancelling a running session", async () => {
    vi.mocked(listAgentTasks)
      .mockResolvedValueOnce([{ id: 41, agent_name: "web-agent", description: "测试004", status: "running", metadata_json: "{\"cwd\":\"/repo\"}" }])
      .mockResolvedValueOnce([{ id: 41, agent_name: "web-agent", description: "测试004", status: "cancelled", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(streamAgentTaskEvents).mockImplementation(async (_identity, _taskId, callbacks) => {
      callbacks.onEvent({ type: "connected" });
    });

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-send-button.danger")?.getAttribute("aria-label")).toContain("Cancel"));

    await act(async () => {
      (host.querySelector(".agent-send-button.danger") as HTMLButtonElement).click();
    });

    await vi.waitFor(() => expect(host.querySelector(".composer-state")?.textContent).toBe("ready"));
    expect(host.querySelector(".agent-send-button")?.getAttribute("aria-label")).toContain("Send");
    expect(host.querySelector("textarea")?.disabled).toBe(false);
  });

  it("returns the composer to ready and reads late next_steps events after completion", async () => {
    const serverEvents = [
      { id: 1, task_id: 60, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"改代码\"}", created_at: "2026-06-30T00:00:00Z" }
    ];
    vi.mocked(listAgentTasks)
      .mockResolvedValueOnce([{ id: 60, agent_name: "web-agent", description: "Late steps", status: "running", metadata_json: "{\"cwd\":\"/repo\"}" }])
      .mockResolvedValue([{ id: 60, agent_name: "web-agent", description: "Late steps", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    const nextStepsEvent = { id: 3, task_id: 60, event_type: "next_steps", payload_json: JSON.stringify({ source: "runner", suggestions: ["跑一遍测试"] }), created_at: "2026-06-30T00:00:04Z" };
    vi.mocked(listAgentTaskEvents).mockImplementation(async (_identity, _taskID, options) => {
      if (typeof options === "object" && options.afterID === 2) {
        return [nextStepsEvent];
      }
      return [...serverEvents];
    });
    let streamCallbacks!: Parameters<typeof streamAgentTaskEvents>[2];
    vi.mocked(streamAgentTaskEvents).mockImplementation((_identity, _taskId, callbacks) => {
      streamCallbacks = callbacks;
      return Promise.resolve();
    });

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(streamCallbacks).toBeDefined());
    const completedEvent = { id: 2, task_id: 60, event_type: "completed", payload_json: "{\"source\":\"runner\",\"response\":\"改完了\",\"next_steps_status\":\"pending\"}", created_at: "2026-06-30T00:00:01Z" };
    serverEvents.push(completedEvent);
    await act(async () => {
      streamCallbacks.onEvent({ type: "connected" });
      streamCallbacks.onEvent({ type: "agent_task_event", event: completedEvent });
      streamCallbacks.onDone();
    });

    await vi.waitFor(() => expect(host.querySelector(".composer-state")?.textContent).toBe("ready"));
    expect(streamAgentTaskEvents).toHaveBeenCalledTimes(1);

    await vi.waitFor(() => expect(vi.mocked(listAgentTaskEvents).mock.calls.some((call) => typeof call[2] === "object" && call[2]?.afterID === 2)).toBe(true));
    await vi.waitFor(() => expect(host.querySelector(".next-step-panel")?.textContent).toContain("跑一遍测试"));
    const nextStepPanel = host.querySelector(".next-step-panel");
    expect(nextStepPanel?.getAttribute("role")).toBe("group");
    expect(nextStepPanel?.getAttribute("aria-label")).toBeTruthy();
    expect(nextStepPanel?.querySelector(".next-step-hint")?.getAttribute("aria-hidden")).toBe("true");
    expect(host.querySelector(".composer-state")?.textContent).toBe("ready");
    const readbackCallCount = vi.mocked(listAgentTaskEvents).mock.calls.filter((call) => typeof call[2] === "object").length;
    await new Promise((resolve) => window.setTimeout(resolve, 350));
    expect(vi.mocked(listAgentTaskEvents).mock.calls.filter((call) => typeof call[2] === "object")).toHaveLength(readbackCallCount);
  });

  it("cancels REST readback when next_steps arrives on SSE", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 73, agent_name: "web-agent", description: "SSE suggestions", status: "running", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([]);
    let streamCallbacks!: Parameters<typeof streamAgentTaskEvents>[2];
    vi.mocked(streamAgentTaskEvents).mockImplementation(async (_identity, _taskID, callbacks) => {
      streamCallbacks = callbacks;
    });
    await act(async () => {
      root.render(<I18nProvider><WebAgentPage identity={identity} onStatus={vi.fn()} /></I18nProvider>);
    });
    await vi.waitFor(() => expect(streamCallbacks).toBeDefined());
    await act(async () => {
      streamCallbacks.onEvent({ type: "agent_task_event", event: { id: 1, task_id: 73, event_type: "completed", payload_json: "{\"next_steps_status\":\"pending\"}" } });
      streamCallbacks.onEvent({ type: "agent_task_event", event: { id: 2, task_id: 73, event_type: "next_steps", payload_json: JSON.stringify({ suggestions: ["来自 SSE"] }) } });
      streamCallbacks.onDone();
    });
    await new Promise((resolve) => window.setTimeout(resolve, 350));
    expect(vi.mocked(listAgentTaskEvents).mock.calls.filter((call) => typeof call[2] === "object")).toHaveLength(0);
  });

  it("keeps late suggestions when the completion refresh snapshot is delayed", async () => {
    const task = { id: 64, agent_name: "web-agent", description: "Delayed refresh", status: "running", metadata_json: "{\"cwd\":\"/repo\"}" };
    vi.mocked(listAgentTasks).mockResolvedValueOnce([task]).mockResolvedValue([{ ...task, status: "completed" }]);
    let detailCalls = 0;
    vi.mocked(getWebAgentConversation).mockImplementation(async () => {
      detailCalls += 1;
      if (detailCalls === 1) {
        return { id: "legacy:0", title: "empty", status: "unknown", latest_task: { id: 0 }, tasks: [], events: [], usage: {} };
      }
      return new Promise(() => undefined);
    });
    const nextStepsEvent = { id: 8, task_id: 64, event_type: "next_steps", payload_json: JSON.stringify({ suggestions: ["保留这条建议"] }), created_at: "2026-06-30T00:00:04Z" };
    vi.mocked(listAgentTaskEvents).mockImplementation(async (_identity, _taskID, options) => {
      if (typeof options === "object") {
        return [nextStepsEvent];
      }
      return [];
    });
    let streamCallbacks!: Parameters<typeof streamAgentTaskEvents>[2];
    vi.mocked(streamAgentTaskEvents).mockImplementation(async (_identity, _taskID, callbacks) => {
      streamCallbacks = callbacks;
    });

    await act(async () => {
      root.render(<I18nProvider><WebAgentPage identity={identity} onStatus={vi.fn()} /></I18nProvider>);
    });
    await vi.waitFor(() => expect(streamCallbacks).toBeDefined());
    await act(async () => {
      streamCallbacks.onEvent({ type: "agent_task_event", event: { id: 7, task_id: 64, event_type: "completed", payload_json: "{\"next_steps_status\":\"pending\"}" } });
      streamCallbacks.onDone();
    });
    await vi.waitFor(() => expect(host.querySelector(".next-step-panel")?.textContent).toContain("保留这条建议"));
  });

  it("reads pending next_steps for an already-terminal task without opening SSE", async () => {
    const completedEvent = { id: 9, task_id: 65, event_type: "completed", payload_json: "{\"next_steps_status\":\"pending\"}" };
    const nextStepsEvent = { id: 10, task_id: 65, event_type: "next_steps", payload_json: JSON.stringify({ suggestions: ["读取已完成任务建议"] }) };
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 65, agent_name: "web-agent", description: "Already complete", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockImplementation(async (_identity, _taskID, options) => typeof options === "object" ? [nextStepsEvent] : [completedEvent]);
    await act(async () => {
      root.render(<I18nProvider><WebAgentPage identity={identity} onStatus={vi.fn()} /></I18nProvider>);
    });
    await vi.waitFor(() => expect(host.querySelector(".next-step-panel")?.textContent).toContain("读取已完成任务建议"));
    expect(streamAgentTaskEvents).not.toHaveBeenCalled();
  });

  it("ignores stale SSE callbacks after an identity switch", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 69, agent_name: "web-agent", description: "Identity stream", status: "running", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    const streams: Array<{ taskID: number; callbacks: Parameters<typeof streamAgentTaskEvents>[2] }> = [];
    vi.mocked(streamAgentTaskEvents).mockImplementation(async (_identity, taskID, callbacks) => {
      streams.push({ taskID, callbacks });
    });
    let updateIdentity!: () => void;
    function IdentityHarness() {
      const [currentIdentity, setCurrentIdentity] = useState(identity);
      updateIdentity = () => setCurrentIdentity((previous) => ({ ...previous, apiToken: "rotated-stream-token" }));
      return <WebAgentPage identity={currentIdentity} onStatus={vi.fn()} />;
    }
    await act(async () => {
      root.render(<I18nProvider><IdentityHarness /></I18nProvider>);
    });
    await vi.waitFor(() => expect(streams).toHaveLength(1));
    await act(async () => updateIdentity());
    await vi.waitFor(() => expect(streams).toHaveLength(2));
    await act(async () => {
      streams[0].callbacks.onEvent({ type: "agent_task_event", event: { id: 99, task_id: 69, event_type: "message", payload_json: "{\"from_agent\":\"assistant\",\"content\":\"stale callback\"}" } });
      streams[0].callbacks.onDone();
    });
    expect(host.textContent).not.toContain("stale callback");
    expect(streams[1].taskID).toBe(69);
  });

  it("does not let an older identity refresh overwrite the current page", async () => {
    let releaseOldStatus!: () => void;
    let statusCalls = 0;
    vi.mocked(getStatus).mockImplementation(async () => {
      statusCalls += 1;
      if (statusCalls === 1) {
        return new Promise((resolve) => { releaseOldStatus = () => resolve({ workspace: "/old" }); });
      }
      return { workspace: "/new" };
    });
    vi.mocked(listAgentTasks).mockImplementation(async (currentIdentity) => [{
      id: currentIdentity.tenantKey === "tenant-b" ? 72 : 71,
      agent_name: "web-agent",
      description: currentIdentity.tenantKey === "tenant-b" ? "Current identity" : "Old identity",
      status: "completed",
      metadata_json: "{\"cwd\":\"/repo\"}"
    }]);
    let updateIdentity!: () => void;
    function IdentityHarness() {
      const [currentIdentity, setCurrentIdentity] = useState(identity);
      updateIdentity = () => setCurrentIdentity({ ...currentIdentity, tenantKey: "tenant-b", userId: "user-b" });
      return <WebAgentPage identity={currentIdentity} onStatus={vi.fn()} />;
    }
    await act(async () => {
      root.render(<I18nProvider><IdentityHarness /></I18nProvider>);
    });
    await vi.waitFor(() => expect(statusCalls).toBe(1));
    await act(async () => updateIdentity());
    releaseOldStatus();
    await vi.waitFor(() => expect(host.textContent).toContain("Current identity"));
    expect(host.textContent).not.toContain("Old identity");
  });

  it("uses the terminal result marker when completed is outside the event page", async () => {
    const pageEvents = Array.from({ length: 500 }, (_, index) => ({ id: index + 1, task_id: 70, event_type: "usage", payload_json: "{}" }));
    const nextStepsEvent = { id: 501, task_id: 70, event_type: "next_steps", payload_json: JSON.stringify({ suggestions: ["读取第 501 条建议"] }) };
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 70, agent_name: "web-agent", description: "Paged complete", status: "completed", result_json: "{\"next_steps_status\":\"pending\"}", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockImplementation(async (_identity, _taskID, options) => typeof options === "object" ? [nextStepsEvent] : pageEvents);
    await act(async () => {
      root.render(<I18nProvider><WebAgentPage identity={identity} onStatus={vi.fn()} /></I18nProvider>);
    });
    await vi.waitFor(() => expect(host.querySelector(".next-step-panel")?.textContent).toContain("读取第 501 条建议"));
    expect(streamAgentTaskEvents).not.toHaveBeenCalled();
  });

  it("does not apply a delayed detail load from a previous session", async () => {
    let releaseFirstLoad!: () => void;
    const firstEvents = new Promise<[]>((resolve) => { releaseFirstLoad = () => resolve([]); });
    vi.mocked(listAgentTasks).mockResolvedValue([
      { id: 66, agent_name: "web-agent", description: "First session", status: "completed", metadata_json: "{\"cwd\":\"/repo/first\"}" },
      { id: 67, agent_name: "web-agent", description: "Second session", status: "completed", metadata_json: "{\"cwd\":\"/repo/second\"}" }
    ]);
    vi.mocked(listAgentTaskEvents).mockImplementation(async (_identity, taskID) => {
      if (taskID === 66) {
        return firstEvents;
      }
      return [{ id: 12, task_id: 67, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"second reply\"}" }];
    });
    await act(async () => {
      root.render(<I18nProvider><WebAgentPage identity={identity} onStatus={vi.fn()} /></I18nProvider>);
    });
    await vi.waitFor(() => expect(host.textContent).toContain("Second session"));
    const second = Array.from(host.querySelectorAll<HTMLButtonElement>(".session-row")).find((button) => button.textContent?.includes("Second session"));
    await act(async () => second?.click());
    await vi.waitFor(() => expect(host.textContent).toContain("second reply"));
    releaseFirstLoad();
    await new Promise((resolve) => window.setTimeout(resolve, 0));
    expect(host.textContent).not.toContain("First session");
    expect(host.textContent).toContain("second reply");
  });

  it("silently times out late next_steps readback without changing completed state", async () => {
    vi.useFakeTimers();
    const onStatus = vi.fn();
    vi.mocked(listAgentTasks)
      .mockResolvedValueOnce([{ id: 61, agent_name: "web-agent", description: "Late steps timeout", status: "running", metadata_json: "{\"cwd\":\"/repo\"}" }])
      .mockResolvedValue([{ id: 61, agent_name: "web-agent", description: "Late steps timeout", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([]);
    let streamCallbacks!: Parameters<typeof streamAgentTaskEvents>[2];
    vi.mocked(streamAgentTaskEvents).mockImplementation(async (_identity, _taskID, callbacks) => {
      streamCallbacks = callbacks;
    });

    try {
      await act(async () => {
        root.render(
          <I18nProvider>
            <WebAgentPage identity={identity} onStatus={onStatus} />
          </I18nProvider>
        );
      });
      await vi.waitFor(() => expect(streamCallbacks).toBeDefined());
      await act(async () => {
        streamCallbacks.onEvent({ type: "agent_task_event", event: {
          id: 4,
          task_id: 61,
          event_type: "completed",
          payload_json: "{\"next_steps_status\":\"pending\"}",
          created_at: "2026-06-30T00:00:01Z"
        } });
        streamCallbacks.onDone();
        await vi.advanceTimersByTimeAsync(6_500);
      });
      expect(host.querySelector(".composer-state")?.textContent).toBe("ready");
      expect(host.querySelector(".next-step-panel")).toBeNull();
      expect(onStatus).not.toHaveBeenCalledWith(expect.stringContaining("timed"));
    } finally {
      vi.useRealTimers();
    }
  });

  it("aborts an in-flight late next_steps request at the readback deadline", async () => {
    vi.useFakeTimers();
    let readbackSignal: AbortSignal | undefined;
    vi.mocked(listAgentTasks)
      .mockResolvedValueOnce([{ id: 62, agent_name: "web-agent", description: "Late steps abort", status: "running", metadata_json: "{\"cwd\":\"/repo\"}" }])
      .mockResolvedValue([{ id: 62, agent_name: "web-agent", description: "Late steps abort", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockImplementation(async (_identity, _taskID, options) => {
      if (typeof options !== "object") {
        return [];
      }
      readbackSignal = options.signal;
      return new Promise<never>((_resolve, reject) => {
        options.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
      });
    });
    let streamCallbacks!: Parameters<typeof streamAgentTaskEvents>[2];
    vi.mocked(streamAgentTaskEvents).mockImplementation(async (_identity, _taskID, callbacks) => {
      streamCallbacks = callbacks;
    });

    try {
      await act(async () => {
        root.render(<I18nProvider><WebAgentPage identity={identity} onStatus={vi.fn()} /></I18nProvider>);
      });
      await vi.waitFor(() => expect(streamCallbacks).toBeDefined());
      await act(async () => {
        streamCallbacks.onEvent({ type: "agent_task_event", event: { id: 5, task_id: 62, event_type: "completed", payload_json: "{\"next_steps_status\":\"pending\"}" } });
        streamCallbacks.onDone();
        await vi.advanceTimersByTimeAsync(250);
      });
      expect(readbackSignal).toBeDefined();
      expect(readbackSignal?.aborted).toBe(false);
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6_000);
      });
      expect(readbackSignal?.aborted).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it("aborts same-task readback when identity changes", async () => {
    vi.useFakeTimers();
    let readbackSignal: AbortSignal | undefined;
    let changeIdentity!: () => void;
    const nextIdentity = { ...identity, apiToken: "rotated-token", mobileJwt: "rotated-mobile-jwt" };
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 63, agent_name: "web-agent", description: "Identity change", status: "running", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockImplementation(async (_identity, _taskID, options) => {
      if (typeof options !== "object") {
        return [];
      }
      readbackSignal = options.signal;
      return new Promise<never>((_resolve, reject) => {
        options.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")), { once: true });
      });
    });
    let streamCallbacks!: Parameters<typeof streamAgentTaskEvents>[2];
    vi.mocked(streamAgentTaskEvents).mockImplementation(async (_identity, _taskID, callbacks) => {
      streamCallbacks = callbacks;
    });
    function IdentityHarness() {
      const [currentIdentity, setCurrentIdentity] = useState(identity);
      changeIdentity = () => setCurrentIdentity(nextIdentity);
      return <WebAgentPage identity={currentIdentity} onStatus={vi.fn()} />;
    }

    try {
      await act(async () => {
        root.render(<I18nProvider><IdentityHarness /></I18nProvider>);
      });
      await vi.waitFor(() => expect(streamCallbacks).toBeDefined());
      await act(async () => {
        streamCallbacks.onEvent({ type: "agent_task_event", event: { id: 6, task_id: 63, event_type: "completed", payload_json: "{\"next_steps_status\":\"pending\"}" } });
        streamCallbacks.onDone();
        await vi.advanceTimersByTimeAsync(250);
      });
      expect(readbackSignal?.aborted).toBe(false);
      await act(async () => changeIdentity());
      expect(readbackSignal?.aborted).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it("sends composer text on Enter and keeps Shift Enter as newline", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 42, agent_name: "web-agent", description: "Ready", status: "ready", metadata_json: "{\"cwd\":\"/repo\"}" }]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const textarea = await vi.waitFor(() => host.querySelector("textarea") as HTMLTextAreaElement);
    await act(async () => {
      setTextareaValue(textarea, "hello");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", shiftKey: true, bubbles: true }));
    });
    expect(sendAgentTaskMessage).not.toHaveBeenCalled();

    await act(async () => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });

    await vi.waitFor(() => expect(sendAgentTaskMessage).toHaveBeenCalledWith(identity, 42, expect.objectContaining({ from_agent: "webui", content: "hello" })));
    expect(vi.mocked(sendAgentTaskMessage).mock.calls[0][2].trace_id).toMatch(/^web-agent-/);
  });

  it("shows slash command suggestions and completes the selected command", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 42, agent_name: "web-agent", description: "Ready", status: "ready", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentSlashCommands).mockResolvedValue([
      { name: "recap", description: "Generate or show the session recap", source: "builtin" },
      { name: "review", description: "Run a code review prompt", source: "builtin" },
      { name: "rewind", description: "Restore code", source: "builtin" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const textarea = await vi.waitFor(() => host.querySelector("textarea") as HTMLTextAreaElement);
    await act(async () => {
      setTextareaValue(textarea, "/re");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });

    await vi.waitFor(() => expect(listAgentSlashCommands).toHaveBeenCalledWith(identity, "/Users/example/GolandProjects/niuma", "re", 8));
    await vi.waitFor(() => expect(host.querySelector(".slash-suggestion-panel")?.textContent).toContain("/review"));
    await act(async () => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
    });
    await vi.waitFor(() => expect(host.querySelector(".slash-suggestion-panel button.active")?.textContent).toContain("/review"));
    await act(async () => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true }));
    });

    expect(textarea.value).toBe("/review ");
    expect(sendAgentTaskMessage).not.toHaveBeenCalled();
  });

  it("keeps a ready blank session out of running activity state", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 43, agent_name: "web-agent", description: "测试005", status: "ready", metadata_json: "{\"cwd\":\"/repo\"}" }]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.textContent).toContain("What should the agent do?"));
    expect(host.querySelector(".agent-send-button.danger")).toBeNull();
    expect(host.textContent).not.toContain("Files read");
    expect(host.textContent).not.toContain("Ran 0 commands");
  });

  it("renders user and assistant messages with separate roles", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 44, agent_name: "web-agent", description: "Chat", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 44, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"hello\"}", trace_id: "webui-test-trace", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 44, event_type: "text_delta", payload_json: "{\"content\":\"hi\"}", trace_id: "webui-test-trace", created_at: "2026-06-30T00:00:01Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-message.user")?.textContent).toContain("hello"));
    expect(host.querySelector(".agent-message.assistant")?.textContent).toContain("hi");
    expect(host.querySelector(".agent-message.user small")?.textContent).not.toContain("webui-test-trace");
    expect(host.querySelector(".agent-message.assistant small")?.textContent).not.toContain("webui-test-trace");
    expect(host.querySelector(".agent-message.user small")?.textContent).not.toMatch(/:00(\\s|$)/);
  });

  it("follows height changes from the latest assistant after the stream closes", async () => {
    let resizeCallback: ResizeObserverCallback | undefined;
    const observe = vi.fn();
    const originalResizeObserver = globalThis.ResizeObserver;
    globalThis.ResizeObserver = class implements ResizeObserver {
      constructor(callback: ResizeObserverCallback) {
        resizeCallback = callback;
      }
      observe = observe;
      unobserve = vi.fn();
      disconnect = vi.fn();
    };
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 144, agent_name: "web-agent", description: "Closed stream", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 144, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"question\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 144, event_type: "text_delta", payload_json: "{\"content\":\"completed response\"}", created_at: "2026-06-30T00:00:01Z" },
      { id: 3, task_id: 144, event_type: "completed", payload_json: "{}", created_at: "2026-06-30T00:00:02Z" }
    ]);

    try {
      await act(async () => {
        root.render(
          <I18nProvider>
            <WebAgentPage identity={identity} onStatus={vi.fn()} />
          </I18nProvider>
        );
      });
      const latestAssistant = await vi.waitFor(() => host.querySelector(".agent-message.assistant") as HTMLElement);
      await vi.waitFor(() => expect(observe).toHaveBeenCalledWith(latestAssistant));
      const container = host.querySelector('[data-testid="agent-conversation-scroll"]') as HTMLDivElement;
      Object.defineProperties(container, {
        scrollHeight: { configurable: true, value: 1_000 },
        scrollTop: { configurable: true, writable: true, value: 0 }
      });

      await act(async () => {
        resizeCallback?.([], {} as ResizeObserver);
      });

      await vi.waitFor(() => expect(container.scrollTop).toBe(1_000));
    } finally {
      globalThis.ResizeObserver = originalResizeObserver;
    }
  });

  it("pauses resize following after an intentional scroll and resumes from jump to latest", async () => {
    let resizeCallback: ResizeObserverCallback | undefined;
    const originalResizeObserver = globalThis.ResizeObserver;
    globalThis.ResizeObserver = class implements ResizeObserver {
      constructor(callback: ResizeObserverCallback) {
        resizeCallback = callback;
      }
      observe = vi.fn();
      unobserve = vi.fn();
      disconnect = vi.fn();
    };
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 145, agent_name: "web-agent", description: "History reading", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 145, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"question\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 145, event_type: "text_delta", payload_json: "{\"content\":\"long response\"}", created_at: "2026-06-30T00:00:01Z" }
    ]);

    try {
      await act(async () => {
        root.render(<I18nProvider><WebAgentPage identity={identity} onStatus={vi.fn()} /></I18nProvider>);
      });
      const container = await vi.waitFor(() => host.querySelector('[data-testid="agent-conversation-scroll"]') as HTMLDivElement);
      Object.defineProperties(container, {
        scrollHeight: { configurable: true, value: 1_000 },
        scrollTop: { configurable: true, writable: true, value: 600 },
        clientHeight: { configurable: true, value: 400 }
      });
      const scrollTo = vi.mocked(Element.prototype.scrollTo);
      // Drain the follow frames queued while mounting before measuring what the
      // resize does. Otherwise how many of them land after mockClear is just
      // machine speed, and the assertion below reads them as a resize scroll.
      await vi.waitFor(() => expect(scrollTo).toHaveBeenCalled());
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 32));
      });
      scrollTo.mockClear();
      await act(async () => container.dispatchEvent(new Event("scroll", { bubbles: true })));
      container.scrollTop = 200;
      await act(async () => container.dispatchEvent(new Event("scroll", { bubbles: true })));
      await act(async () => resizeCallback?.([], {} as ResizeObserver));
      expect(scrollTo).not.toHaveBeenCalled();

      const jump = await vi.waitFor(() => {
        const button = host.querySelector('button[aria-label="Jump to latest"]');
        expect(button).not.toBeNull();
        return button as HTMLButtonElement;
      });
      expect(jump.textContent).toBe("");
      expect(jump.querySelector("svg")).not.toBeNull();
      expect(container.contains(jump)).toBe(false);
      await act(async () => jump.click());
      expect(scrollTo).toHaveBeenCalled();
      scrollTo.mockClear();
      await act(async () => resizeCallback?.([], {} as ResizeObserver));
      await vi.waitFor(() => expect(container.scrollTop).toBe(1_000));
    } finally {
      globalThis.ResizeObserver = originalResizeObserver;
    }
  });

  it("coalesces resize scrolling and cleans observer and pending frame on unmount", async () => {
    let resizeCallback: ResizeObserverCallback | undefined;
    let animationFrameCallback: FrameRequestCallback | undefined;
    const disconnect = vi.fn();
    const originalResizeObserver = globalThis.ResizeObserver;
    const originalRequestAnimationFrame = globalThis.requestAnimationFrame;
    const originalCancelAnimationFrame = globalThis.cancelAnimationFrame;
    globalThis.ResizeObserver = class implements ResizeObserver {
      constructor(callback: ResizeObserverCallback) {
        resizeCallback = callback;
      }
      observe = vi.fn();
      unobserve = vi.fn();
      disconnect = disconnect;
    };
    globalThis.requestAnimationFrame = vi.fn((callback: FrameRequestCallback) => {
      animationFrameCallback = callback;
      return 73;
    });
    globalThis.cancelAnimationFrame = vi.fn();
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 146, agent_name: "web-agent", description: "Cleanup", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 146, event_type: "text_delta", payload_json: "{\"content\":\"response\"}", created_at: "2026-06-30T00:00:01Z" }
    ]);

    try {
      await act(async () => {
        root.render(<I18nProvider><WebAgentPage identity={identity} onStatus={vi.fn()} /></I18nProvider>);
      });
      await vi.waitFor(() => expect(resizeCallback).toBeTypeOf("function"));
      vi.mocked(globalThis.requestAnimationFrame).mockClear();
      await act(async () => {
        resizeCallback?.([], {} as ResizeObserver);
        resizeCallback?.([], {} as ResizeObserver);
      });
      expect(globalThis.requestAnimationFrame).toHaveBeenCalledTimes(1);
      expect(animationFrameCallback).toBeTypeOf("function");

      await act(async () => root.unmount());
      expect(disconnect).toHaveBeenCalledTimes(1);
      expect(globalThis.cancelAnimationFrame).toHaveBeenCalledWith(73);
      root = createRoot(host);
    } finally {
      globalThis.ResizeObserver = originalResizeObserver;
      globalThis.requestAnimationFrame = originalRequestAnimationFrame;
      globalThis.cancelAnimationFrame = originalCancelAnimationFrame;
    }
  });

  it("preserves spaces between streamed assistant deltas", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 48, agent_name: "web-agent", description: "Stream", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 48, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"hello\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 48, event_type: "text_delta", payload_json: "{\"content\":\"web \"}", created_at: "2026-06-30T00:00:01Z" },
      { id: 3, task_id: 48, event_type: "text_delta", payload_json: "{\"content\":\"agent \"}", created_at: "2026-06-30T00:00:02Z" },
      { id: 4, task_id: 48, event_type: "text_delta", payload_json: "{\"content\":\"ok\"}", created_at: "2026-06-30T00:00:03Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-message.assistant")?.textContent).toContain("web agent ok"));
  });

  it("uses completed task result response when replayed events are partial", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{
      id: 59,
      agent_name: "web-agent",
      description: "Partial replay",
      status: "completed",
      metadata_json: "{\"cwd\":\"/repo\"}",
      result_json: "{\"response\":\"partial complete answer\"}"
    }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 59, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"question\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 59, event_type: "text_delta", payload_json: "{\"content\":\"partial\"}", created_at: "2026-06-30T00:00:01Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-message.assistant")?.textContent).toContain("partial complete answer"));
  });

  it("does not render zero operational activity for text-only replies", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 49, agent_name: "web-agent", description: "Text only", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 49, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"hello\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 49, event_type: "text_delta", payload_json: "{\"content\":\"plain reply\"}", created_at: "2026-06-30T00:00:01Z" },
      { id: 3, task_id: 49, event_type: "completed", payload_json: "{\"source\":\"runner\"}", created_at: "2026-06-30T00:00:02Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-message.assistant")?.textContent).toContain("plain reply"));
    expect(host.textContent).not.toContain("Files read");
    expect(host.textContent).not.toContain("Ran 0 commands");
    expect(host.textContent).not.toContain("0 files");
  });

  it("attaches expandable command runs to the assistant message", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 50, agent_name: "web-agent", description: "File work", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 50, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"inspect\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 50, event_type: "tool_call", payload_json: "{\"tool_id\":\"b1\",\"tool_name\":\"Bash\",\"input\":\"{\\\"command\\\":\\\"go test ./...\\\"}\"}", created_at: "2026-06-30T00:00:01Z" },
      { id: 3, task_id: 50, event_type: "tool_result", payload_json: "{\"tool_id\":\"b1\",\"tool_name\":\"Bash\",\"preview\":\"ok\"}", created_at: "2026-06-30T00:00:03Z" },
      { id: 4, task_id: 50, event_type: "tool_call", payload_json: "{\"tool_id\":\"r1\",\"tool_name\":\"Read\",\"input\":\"{\\\"file_path\\\":\\\"/repo/main.go\\\"}\"}", created_at: "2026-06-30T00:00:04Z" },
      { id: 5, task_id: 50, event_type: "tool_result", payload_json: "{\"tool_id\":\"r1\",\"tool_name\":\"Read\"}", created_at: "2026-06-30T00:00:05Z" },
      { id: 6, task_id: 50, event_type: "text_delta", payload_json: "{\"content\":\"done\"}", created_at: "2026-06-30T00:00:06Z" },
      { id: 7, task_id: 50, event_type: "completed", payload_json: "{}", created_at: "2026-06-30T00:00:07Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-message.assistant .agent-msg-tools")).not.toBeNull());
    const toolsBlock = host.querySelector(".agent-message.assistant .agent-msg-tools") as HTMLElement;
    expect(toolsBlock.querySelector("summary")?.textContent).toContain("Ran 2 commands");
    expect(toolsBlock.textContent).toContain("go test ./...");
    expect(toolsBlock.textContent).toContain("/repo/main.go");
    const bashRow = Array.from(toolsBlock.querySelectorAll(".agent-msg-tool-row")).find((row) => row.textContent?.includes("go test"));
    expect(bashRow?.textContent).toContain("2s");
    // 旧的顶部活动条已移除
    expect(host.querySelector(".agent-activity-rows")).toBeNull();
  });

  it("shows multiline tool output inside the expanded command runs", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 51, agent_name: "web-agent", description: "Diff work", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 51, event_type: "message", payload_json: JSON.stringify({ from_agent: "webui", content: "show diff" }), created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 51, event_type: "tool_call", payload_json: JSON.stringify({ tool_id: "b1", tool_name: "Bash", input: "{\"command\":\"git diff\"}" }), created_at: "2026-06-30T00:00:01Z" },
      {
        id: 3,
        task_id: 51,
        event_type: "tool_result",
        payload_json: JSON.stringify({
          tool_id: "b1",
          tool_name: "Bash",
          preview: "diff --git a/hooks/session-start.sh b/hooks...",
          output: "diff --git a/hooks/session-start.sh b/hooks/session-start.sh\n-old line\n+new line"
        }),
        created_at: "2026-06-30T00:00:02Z"
      },
      { id: 4, task_id: 51, event_type: "text_delta", payload_json: JSON.stringify({ content: "done" }), created_at: "2026-06-30T00:00:03Z" },
      { id: 5, task_id: 51, event_type: "completed", payload_json: "{}", created_at: "2026-06-30T00:00:04Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-message.assistant .agent-msg-tools")).not.toBeNull());
    const toolsBlock = host.querySelector(".agent-message.assistant .agent-msg-tools") as HTMLElement;
    const outputBlock = toolsBlock.querySelector(".agent-msg-tool-output");
    expect(outputBlock).not.toBeNull();
    // 完整 output 优先于 160 字符的 preview,且保留多行
    expect(outputBlock?.textContent).toContain("-old line");
    expect(outputBlock?.textContent).toContain("+new line");
  });

  it("extracts the command from server-truncated invalid tool input JSON", async () => {
    // 后端把超过 600 字符的 input JSON 按 rune 截断并追加 "...",产生非法 JSON;
    // 命令列应提取出主字段值,而不是把乱码 JSON 前缀原样展示。
    const truncatedInput = "{\"command\":\"go test ./internal/query/ -run TestFoo\",\"description\":\"run the fu...";
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 53, agent_name: "web-agent", description: "Big input", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 53, event_type: "message", payload_json: JSON.stringify({ from_agent: "webui", content: "run tests" }), created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 53, event_type: "tool_call", payload_json: JSON.stringify({ tool_id: "b1", tool_name: "Bash", input: truncatedInput }), created_at: "2026-06-30T00:00:01Z" },
      { id: 3, task_id: 53, event_type: "tool_result", payload_json: JSON.stringify({ tool_id: "b1", tool_name: "Bash", input: truncatedInput, preview: "ok", output: "ok" }), created_at: "2026-06-30T00:00:02Z" },
      { id: 4, task_id: 53, event_type: "text_delta", payload_json: JSON.stringify({ content: "done" }), created_at: "2026-06-30T00:00:03Z" },
      { id: 5, task_id: 53, event_type: "completed", payload_json: "{}", created_at: "2026-06-30T00:00:04Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-message.assistant .agent-msg-tools")).not.toBeNull());
    const command = host.querySelector(".agent-message.assistant .agent-msg-tool-command") as HTMLElement;
    expect(command.textContent).toContain("go test ./internal/query/ -run TestFoo");
    expect(command.textContent).not.toContain("{\"command\"");
  });

  it("renders paired tool activity cards in progress", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 52, agent_name: "web-agent", description: "Tool work", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 52, event_type: "tool_call", payload_json: "{\"tool_name\":\"Bash\",\"tool_id\":\"toolu_1\",\"turn\":1}", created_at: "2026-06-30T00:00:01Z" },
      { id: 2, task_id: 52, event_type: "tool_result", payload_json: "{\"tool_name\":\"Bash\",\"tool_id\":\"toolu_1\",\"turn\":1,\"preview\":\"go test ./... ok\",\"is_error\":false}", created_at: "2026-06-30T00:00:04Z" },
      { id: 3, task_id: 52, event_type: "tool_call", payload_json: "{\"tool_name\":\"Read\",\"tool_id\":\"toolu_2\",\"turn\":1}", created_at: "2026-06-30T00:00:05Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await act(async () => {
      host.querySelector<HTMLButtonElement>("[title='Toggle right panel']")?.click();
    });
    await vi.waitFor(() => expect(host.textContent).toContain("Tool activity"));
    const cards = host.querySelectorAll(".tool-activity-card");
    expect(cards).toHaveLength(2);
    expect(cards[0].textContent).toContain("Bash");
    expect(cards[0].textContent).toContain("done");
    expect(cards[0].textContent).toContain("3s");
    expect(cards[0].textContent).toContain("go test ./... ok");
    expect(cards[1].textContent).toContain("Read");
    expect(cards[1].textContent).toContain("running");
  });

  it("renders capability loop summaries from tool result wrappers in progress", async () => {
    const output = [
      `subagent raw answer ${"hidden detail ".repeat(40)}`,
      "<capability_loop>",
      JSON.stringify({
        capability_loop: {
          evidence: ["web/src/components/WebAgentPage.tsx surfaces Task evidence"],
          assumptions: ["parent sees tool_result output"],
          unknowns: ["browser baseline not sampled"],
          verification: ["npm test -- WebAgentPage.test.tsx"],
          risks: ["preview can truncate wrapper fields"],
          next_action: "continue from WebUI-visible evidence",
          follow_up_id: "tool:toolu_pending",
          resolved_follow_up: "resolved pending WebUI inspection",
          supersedes_evidence_id: "tool:toolu_pending",
          supersedes_evidence_ids: ["task:1"]
        }
      }),
      "</capability_loop>",
      "These structured fields are parent decision context; carry evidence, assumptions, unknowns, verification, risks, and next_action forward."
    ].join("\n");
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 63, agent_name: "web-agent", description: "Capability Loop", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 63, event_type: "tool_call", payload_json: JSON.stringify({ tool_name: "Task", tool_id: "toolu_task", turn: 1 }), created_at: "2026-06-30T00:00:01Z" },
      { id: 2, task_id: 63, event_type: "tool_result", payload_json: JSON.stringify({ tool_name: "Task", tool_id: "toolu_task", turn: 1, preview: "completed", output, is_error: false }), created_at: "2026-06-30T00:00:04Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await act(async () => {
      host.querySelector<HTMLButtonElement>("[title='Toggle right panel']")?.click();
    });
    await vi.waitFor(() => expect(host.textContent).toContain("Tool activity"));
    const recent = host.querySelector(".recent-agent-evidence");
    expect(recent?.textContent).toContain("Recent agent evidence");
    expect(recent?.textContent).toContain("capability_loop:");
    expect(recent?.textContent).toContain("evidence: web/src/components/WebAgentPage.tsx surfaces Task evidence");
    expect(recent?.textContent).toContain("risks: preview can truncate wrapper fields");
    expect(recent?.textContent).toContain("next_action: continue from WebUI-visible evidence");
    expect(recent?.textContent).toContain("follow_up_id: tool:toolu_pending");
    expect(recent?.textContent).toContain("resolved_follow_up: resolved pending WebUI inspection");
    expect(recent?.textContent).toContain("supersedes_evidence_id: tool:toolu_pending,task:1");
    expect(recent?.textContent).not.toContain("hidden detail");
    expect(recent?.textContent).not.toContain("parent decision context");
    const card = host.querySelector(".tool-activity-card");
    expect(card?.textContent).toContain("Task");
    expect(card?.textContent).toContain("capability_loop:");
    expect(card?.textContent).toContain("evidence: web/src/components/WebAgentPage.tsx surfaces Task evidence");
    expect(card?.textContent).toContain("unknowns: browser baseline not sampled");
    expect(card?.textContent).toContain("next_action: continue from WebUI-visible evidence");
    expect(card?.textContent).toContain("follow_up_id: tool:toolu_pending");
    expect(card?.textContent).toContain("resolved_follow_up: resolved pending WebUI inspection");
    expect(card?.textContent).toContain("supersedes_evidence_id: tool:toolu_pending,task:1");
    expect(card?.textContent).not.toContain("hidden detail");
    expect(card?.textContent).not.toContain("parent decision context");
  });

  it("skips placeholder capability loop fields in progress", async () => {
    const output = [
      "<capability_loop>",
      JSON.stringify({
        capability_loop: {
          evidence: ["WEB_PLACEHOLDER_EVIDENCE"],
          assumptions: ["None observed"],
          unknowns: ["None observed"],
          verification: ["None observed"],
          risks: ["None observed"],
          next_action: "WEB_PLACEHOLDER_NEXT_ACTION"
        }
      }),
      "</capability_loop>"
    ].join("\n");
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 66, agent_name: "web-agent", description: "Placeholder fields", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 66, event_type: "tool_call", payload_json: JSON.stringify({ tool_name: "Task", tool_id: "toolu_task_placeholder", turn: 1 }), created_at: "2026-06-30T00:00:01Z" },
      { id: 2, task_id: 66, event_type: "tool_result", payload_json: JSON.stringify({ tool_name: "Task", tool_id: "toolu_task_placeholder", turn: 1, output, is_error: false }), created_at: "2026-06-30T00:00:04Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await act(async () => {
      host.querySelector<HTMLButtonElement>("[title='Toggle right panel']")?.click();
    });
    await vi.waitFor(() => expect(host.textContent).toContain("Recent agent evidence"));
    const recent = host.querySelector(".recent-agent-evidence");
    expect(recent?.textContent).toContain("WEB_PLACEHOLDER_EVIDENCE");
    expect(recent?.textContent).toContain("WEB_PLACEHOLDER_NEXT_ACTION");
    expect(recent?.textContent).not.toContain("None observed");
    expect(recent?.textContent).not.toContain("assumptions:");
    expect(recent?.textContent).not.toContain("unknowns:");
    expect(recent?.textContent).not.toContain("verification:");
    expect(recent?.textContent).not.toContain("risks:");
    const card = host.querySelector(".tool-activity-card");
    expect(card?.textContent).toContain("WEB_PLACEHOLDER_EVIDENCE");
    expect(card?.textContent).toContain("WEB_PLACEHOLDER_NEXT_ACTION");
    expect(card?.textContent).not.toContain("None observed");
  });

  it("skips default completed next action in capability loop progress", async () => {
    const output = [
      "<capability_loop>",
      JSON.stringify({
        capability_loop: {
          evidence: ["WEB_DEFAULT_NEXT_ACTION_EVIDENCE"],
          next_action: "Parent agent should synthesize the sub-agent result against the user's goal and verify any unproven claims before finalizing."
        }
      }),
      "</capability_loop>"
    ].join("\n");
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 67, agent_name: "web-agent", description: "Default next action", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 67, event_type: "tool_call", payload_json: JSON.stringify({ tool_name: "Task", tool_id: "toolu_task_default_next", turn: 1 }), created_at: "2026-06-30T00:00:01Z" },
      { id: 2, task_id: 67, event_type: "tool_result", payload_json: JSON.stringify({ tool_name: "Task", tool_id: "toolu_task_default_next", turn: 1, output, is_error: false }), created_at: "2026-06-30T00:00:04Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await act(async () => {
      host.querySelector<HTMLButtonElement>("[title='Toggle right panel']")?.click();
    });
    await vi.waitFor(() => expect(host.textContent).toContain("Recent agent evidence"));
    const recent = host.querySelector(".recent-agent-evidence");
    expect(recent?.textContent).toContain("WEB_DEFAULT_NEXT_ACTION_EVIDENCE");
    expect(recent?.textContent).not.toContain("next_action:");
    expect(recent?.textContent).not.toContain("Parent agent should synthesize");
  });

  it("renders sourced failed AgentGet evidence in progress", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 64, agent_name: "web-agent", description: "Failed evidence", status: "failed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 64, event_type: "tool_call", payload_json: JSON.stringify({ tool_name: "AgentGet", tool_id: "toolu_agent_get", turn: 2 }), created_at: "2026-06-30T00:00:01Z" },
      {
        id: 2,
        task_id: 64,
        event_type: "tool_result",
        payload_json: JSON.stringify({
          tool_name: "AgentGet",
          tool_id: "toolu_agent_get",
          turn: 2,
          is_error: false,
          evidence_source: "agent_get",
          task: { id: 43, agent_name: "auditor", description: "failed recovery probe", status: "failed" },
          result: {
            status: "failed",
            session_id: "failed-web-session",
            transcript_path: "/tmp/failed-web.jsonl",
            output_file: "/tmp/failed-web.output",
            worktree_path: "/tmp/failed-web-worktree",
            worktree_branch: "agent/failed-web",
            capability_loop: {
              evidence: ["FAILED_WEB_VISIBLE_EVIDENCE"],
              risks: ["FAILED_WEB_VISIBLE_RISK"],
              next_action: "FAILED_WEB_VISIBLE_NEXT_ACTION"
            }
          }
        }),
        created_at: "2026-06-30T00:00:04Z"
      }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await act(async () => {
      host.querySelector<HTMLButtonElement>("[title='Toggle right panel']")?.click();
    });
    await vi.waitFor(() => expect(host.textContent).toContain("Recent agent evidence"));
    const recent = host.querySelector(".recent-agent-evidence");
    expect(recent?.textContent).toContain("source: agent_get | AgentGet #43 auditor failed: failed recovery probe | capability_loop:");
    expect(recent?.textContent).toContain("FAILED_WEB_VISIBLE_EVIDENCE");
    expect(recent?.textContent).toContain("FAILED_WEB_VISIBLE_RISK");
    expect(recent?.textContent).toContain("FAILED_WEB_VISIBLE_NEXT_ACTION");
    expect(recent?.textContent).toContain("artifacts:");
    expect(recent?.textContent).toContain("transcript_path: /tmp/failed-web.jsonl");
    expect(recent?.textContent).toContain("output_file: /tmp/failed-web.output");
    expect(recent?.textContent).toContain("worktree_path: /tmp/failed-web-worktree");
    expect(recent?.textContent).toContain("worktree_branch: agent/failed-web");
    const card = host.querySelector(".tool-activity-card");
    expect(card?.textContent).toContain("AgentGet");
    expect(card?.textContent).toContain("capability_loop:");
    expect(card?.textContent).not.toContain("AgentGet #43 auditor failed");
  });

  it("renders sourced failed Task wrapper evidence in progress", async () => {
    const output = [
      "partial sub-agent answer",
      "<capability_loop>",
      JSON.stringify({
        evidence_source: "terminal_agent_task_store",
        status: "failed",
        description: "partial stream audit",
        session_id: "partial-web-session",
        transcript_path: "/tmp/partial-web.jsonl",
        output_file: "/tmp/partial-web.output",
        worktree_path: "/tmp/partial-web-worktree",
        worktree_branch: "agent/partial-web",
        capability_loop: {
          evidence: ["PARTIAL_WEB_VISIBLE_EVIDENCE"],
          risks: ["PARTIAL_WEB_VISIBLE_RISK"],
          next_action: "PARTIAL_WEB_VISIBLE_NEXT_ACTION"
        }
      }),
      "</capability_loop>"
    ].join("\n");
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 65, agent_name: "web-agent", description: "Partial Task", status: "failed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 65, event_type: "tool_call", payload_json: JSON.stringify({ tool_name: "Task", tool_id: "toolu_task_failed", turn: 1 }), created_at: "2026-06-30T00:00:01Z" },
      { id: 2, task_id: 65, event_type: "tool_result", payload_json: JSON.stringify({ tool_name: "Task", tool_id: "toolu_task_failed", turn: 1, output, is_error: false }), created_at: "2026-06-30T00:00:04Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await act(async () => {
      host.querySelector<HTMLButtonElement>("[title='Toggle right panel']")?.click();
    });
    await vi.waitFor(() => expect(host.textContent).toContain("Recent agent evidence"));
    const recent = host.querySelector(".recent-agent-evidence");
    expect(recent?.textContent).toContain("source: task_store | Task failed: partial stream audit | capability_loop:");
    expect(recent?.textContent).toContain("PARTIAL_WEB_VISIBLE_EVIDENCE");
    expect(recent?.textContent).toContain("PARTIAL_WEB_VISIBLE_RISK");
    expect(recent?.textContent).toContain("PARTIAL_WEB_VISIBLE_NEXT_ACTION");
    expect(recent?.textContent).toContain("artifacts:");
    expect(recent?.textContent).toContain("transcript_path: /tmp/partial-web.jsonl");
    expect(recent?.textContent).toContain("output_file: /tmp/partial-web.output");
    expect(recent?.textContent).toContain("worktree_path: /tmp/partial-web-worktree");
    expect(recent?.textContent).toContain("worktree_branch: agent/partial-web");
    expect(recent?.textContent).not.toContain("partial sub-agent answer");
  });

  it("renders assistant markdown as structured content", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 51, agent_name: "web-agent", description: "Markdown", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 51, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"render markdown\"}", created_at: "2026-06-30T00:00:00Z" },
      {
        id: 2,
        task_id: 51,
        event_type: "text_delta",
        payload_json: JSON.stringify({
          content: "## Plan\n\n> important\n\n- [x] Done\n- Next with **bold** and `code`\n\n```go\nfmt.Println(\"ok\")\n```"
        }),
        created_at: "2026-06-30T00:00:01Z"
      }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-markdown h2")?.textContent).toBe("Plan"));
    expect(host.querySelector(".agent-markdown blockquote")?.textContent).toContain("important");
    expect(host.querySelector(".agent-markdown input[type='checkbox']")).not.toBeNull();
    expect(host.querySelector(".agent-markdown strong")?.textContent).toBe("bold");
    expect(host.querySelector(".agent-markdown code")?.textContent).toBe("code");
    expect(host.querySelector(".agent-markdown pre code")?.textContent).toContain("fmt.Println");
  });

  it("renders loose same-line fenced code as a block", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 61, agent_name: "web-agent", description: "Loose Fence", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 61, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"render loose fence\"}", created_at: "2026-06-30T00:00:00Z" },
      {
        id: 2,
        task_id: 61,
        event_type: "text_delta",
        payload_json: JSON.stringify({ content: "Output:\n```textTODO049_PERMISSION_REAL```" }),
        created_at: "2026-06-30T00:00:01Z"
      }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-markdown pre code")?.textContent).toBe("TODO049_PERMISSION_REAL"));
    expect(host.querySelector(".agent-markdown p code")?.textContent || "").not.toContain("TODO049_PERMISSION_REAL");
  });

  it("renders the live assistant reply as progressive plain text before final markdown", async () => {
    vi.useFakeTimers();
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 56, agent_name: "web-agent", description: "Live Markdown", status: "running", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 56, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"render live markdown\"}", created_at: "2026-06-30T00:00:00Z" },
      {
        id: 2,
        task_id: 56,
        event_type: "text_delta",
        payload_json: JSON.stringify({ content: "## Plan\n\n- First\n- Second" }),
        created_at: "2026-06-30T00:00:01Z"
      }
    ]);
    vi.mocked(streamAgentTaskEvents).mockImplementation(async (_identity, _taskId, callbacks) => {
      callbacks.onEvent({ type: "connected" });
    });

    try {
      await act(async () => {
        root.render(
          <I18nProvider>
            <WebAgentPage identity={identity} onStatus={vi.fn()} />
          </I18nProvider>
        );
      });

      await vi.waitFor(() => expect(host.querySelector(".agent-message.assistant.live .agent-markdown.live-markdown")).not.toBeNull());

      await act(async () => {
        vi.advanceTimersByTime(700);
      });

      await vi.waitFor(() => expect(host.querySelector(".agent-message.assistant.live .agent-markdown h2")?.textContent).toContain("Plan"));
      expect(host.querySelectorAll(".agent-message.assistant.live .agent-markdown li")).toHaveLength(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it("tolerates loose assistant markdown headings outside code fences", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 54, agent_name: "web-agent", description: "Loose Markdown", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 54, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"render loose markdown\"}", created_at: "2026-06-30T00:00:00Z" },
      {
        id: 2,
        task_id: 54,
        event_type: "text_delta",
        payload_json: JSON.stringify({
          content: [
            "我可以做的事大致分几类：",
            "",
            "##代码开发- 阅读、理解、修改 Go/前端/脚本等项目代码## 调试与排查- 系统化调查报错、异常行为##项目工作流- 查看 git 状态、diff、历史",
            "",
            "```md",
            "##代码块里保持原样",
            "```"
          ].join("\n")
        }),
        created_at: "2026-06-30T00:00:01Z"
      }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => {
      const headings = Array.from(host.querySelectorAll(".agent-markdown h2")).map((node) => node.textContent);
      expect(headings).toEqual(["代码开发", "调试与排查", "项目工作流"]);
    });
    expect(host.querySelector(".agent-markdown")?.textContent).not.toContain("##代码开发");
    expect(host.querySelector(".agent-markdown ul")?.textContent).toContain("阅读、理解、修改");
    expect(host.querySelector(".agent-markdown pre code")?.textContent).toContain("##代码块里保持原样");
  });

  it("recovers compact unordered and ordered markdown lists outside code spans", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 55, agent_name: "web-agent", description: "Compact Lists", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 55, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"render compact lists\"}", created_at: "2026-06-30T00:00:00Z" },
      {
        id: 2,
        task_id: 55,
        event_type: "text_delta",
        payload_json: JSON.stringify({
          content: "- A 提交全部改动- B 暂存改动，完成后恢复- C 暂停检查\n1. 先读取文件 2. 再运行测试 3. 最后汇报\n- 这是一句普通文本 - 后半句仍属于同一项\n普通文本中的 `- not a list -` 应保持原样"
        }),
        created_at: "2026-06-30T00:00:01Z"
      }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelectorAll(".agent-markdown ul")).toHaveLength(2));
    expect(host.querySelectorAll(".agent-markdown ul")[0].querySelectorAll("li")).toHaveLength(3);
    expect(Array.from(host.querySelectorAll(".agent-markdown ul")[0].querySelectorAll("li")).map((node) => node.textContent)).toEqual([
      "A 提交全部改动",
      "B 暂存改动，完成后恢复",
      "C 暂停检查"
    ]);
    expect(host.querySelectorAll(".agent-markdown ul")[1].querySelectorAll("li")).toHaveLength(1);
    expect(host.querySelectorAll(".agent-markdown ul")[1].querySelector("li")?.textContent).toBe("这是一句普通文本 - 后半句仍属于同一项");
    expect(host.querySelectorAll(".agent-markdown ol li")).toHaveLength(3);
    const assistantMarkdown = host.querySelector(".agent-message.assistant .agent-markdown");
    expect(assistantMarkdown?.textContent).toContain("- not a list -");
    expect(assistantMarkdown?.querySelector("ul li")?.textContent).not.toContain("not a list");
  });

  it("keeps compact list markers inside fenced code untouched", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 57, agent_name: "web-agent", description: "Compact Code", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 57, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"render compact code\"}", created_at: "2026-06-30T00:00:00Z" },
      {
        id: 2,
        task_id: 57,
        event_type: "text_delta",
        payload_json: JSON.stringify({ content: "```text\n- first- second- third\n```" }),
        created_at: "2026-06-30T00:00:01Z"
      }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-markdown pre code")?.textContent).toBe("- first- second- third"));
    expect(host.querySelectorAll(".agent-markdown ul li")).toHaveLength(0);
  });

  it("shows non-zero context percent from token usage and context length", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{
      id: 53,
      agent_name: "web-agent",
      description: "Usage",
      status: "completed",
      metadata_json: "{\"cwd\":\"/repo\"}",
      result_json: "{\"total_tokens\":2500,\"context_length\":100000}"
    }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.textContent).toContain("Context 3%"));
  });

  it("shows prompt cache hit rate from token usage", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{
      id: 54,
      agent_name: "web-agent",
      description: "Cache hit",
      status: "completed",
      metadata_json: "{\"cwd\":\"/repo\"}",
      result_json: "{\"input_tokens\":1000,\"cache_read_input_tokens\":800,\"cache_creation_input_tokens\":200}"
    }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.textContent).toContain("Hit 80%"));
  });

  it("renders thinking delta and compact summary events in the conversation", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{
      id: 57,
      agent_name: "web-agent",
      description: "P1 events",
      status: "completed",
      metadata_json: "{\"cwd\":\"/repo\"}",
      result_json: "{\"response\":\"final answer\"}"
    }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 57, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"work\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 57, event_type: "thinking_delta", payload_json: "{\"content\":\"checking repo state\"}", created_at: "2026-06-30T00:00:01Z" },
      { id: 3, task_id: 57, event_type: "compact_summary", payload_json: "{\"summary\":\"kept project facts\"}", created_at: "2026-06-30T00:00:02Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.textContent).toContain("checking repo state"));
    expect(host.textContent).toContain("Compact summary: kept project facts");
  });

  it("hides thinking conversation content when webAgentUI.showThinking is false", async () => {
    vi.mocked(getGlobalSettings).mockResolvedValue({
      path: "/tmp/settings.json",
      exists: true,
      doc: { webAgentUI: { showThinking: false } },
      masked: []
    });
    vi.mocked(listAgentTasks).mockResolvedValue([{
      id: 157,
      agent_name: "web-agent",
      description: "Hidden thinking",
      status: "completed",
      metadata_json: "{\"cwd\":\"/repo\"}",
      result_json: "{\"response\":\"final answer\"}"
    }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 157, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"work\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 157, event_type: "thinking_delta", payload_json: "{\"content\":\"private reasoning\"}", created_at: "2026-06-30T00:00:01Z" },
      { id: 3, task_id: 157, event_type: "compact_summary", payload_json: "{\"summary\":\"visible summary\"}", created_at: "2026-06-30T00:00:02Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const conversation = await vi.waitFor(() => {
      const node = host.querySelector(".agent-conversation-scroll");
      expect(node?.textContent).toContain("visible summary");
      return node;
    });
    expect(conversation?.textContent).not.toContain("private reasoning");
    expect(listAgentTaskEvents).toHaveBeenCalled();
  });

  it("renders configured thinking summaries collapsed and expands the full reasoning on demand", async () => {
    vi.mocked(getGlobalSettings).mockResolvedValue({
      path: "/tmp/settings.json",
      exists: true,
      doc: { webAgentUI: { thinkingMode: "summary" } },
      masked: []
    });
    vi.mocked(listAgentTasks).mockResolvedValue([{
      id: 159,
      agent_name: "web-agent",
      description: "Collapsible thinking",
      status: "completed",
      metadata_json: "{\"cwd\":\"/repo\"}",
      result_json: "{\"response\":\"final answer\"}"
    }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 159, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"work\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 159, event_type: "thinking_delta", payload_json: "{\"content\":\"private reasoning\"}", created_at: "2026-06-30T00:00:01Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const details = await vi.waitFor(() => {
      const node = host.querySelector<HTMLDetailsElement>("details.agent-thinking-details");
      expect(node).not.toBeNull();
      expect(node?.open).toBe(false);
      expect(node?.textContent).toContain("Expand thinking");
      return node as HTMLDetailsElement;
    });
    await act(async () => details.querySelector("summary")?.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(details.open).toBe(true);
    expect(details.textContent).toContain("private reasoning");
    expect(details.textContent).toContain("Collapse thinking");
  });

  it("keeps thinking delta word boundaries and removes completed thinking status residue", async () => {
    vi.mocked(getGlobalSettings).mockResolvedValue({
      path: "/tmp/settings.json",
      exists: true,
      doc: { webAgentUI: { thinkingMode: "summary" } },
      masked: []
    });
    vi.mocked(listAgentTasks).mockResolvedValue([{
      id: 163,
      agent_name: "web-agent",
      description: "Thinking status",
      status: "completed",
      metadata_json: "{\"cwd\":\"/repo\"}",
      result_json: "{\"response\":\"final answer\"}"
    }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 163, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"work\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 163, event_type: "thinking_delta", payload_json: "{\"content\":\"The user asks\"}", created_at: "2026-06-30T00:00:01Z" },
      { id: 3, task_id: 163, event_type: "thinking_delta", payload_json: "{\"content\":\"again about model\"}", created_at: "2026-06-30T00:00:02Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const details = await vi.waitFor(() => host.querySelector<HTMLDetailsElement>("details.agent-thinking-details"));
    expect(details?.textContent).toContain("The user asks again about model");
    expect(details?.textContent).toContain("Completed");
    expect(details?.textContent).not.toMatch(/·\s*thinking/i);
  });

  it("defaults an unconfigured web agent to collapsed thinking summaries", async () => {
    vi.mocked(getGlobalSettings).mockResolvedValue({ path: "", exists: false, doc: {}, masked: [] });
    vi.mocked(listAgentTasks).mockResolvedValue([{
      id: 164,
      agent_name: "web-agent",
      description: "Default thinking mode",
      status: "completed",
      metadata_json: "{\"cwd\":\"/repo\"}",
      result_json: "{\"response\":\"final answer\"}"
    }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 164, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"work\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 164, event_type: "thinking_delta", payload_json: "{\"content\":\"private reasoning\"}", created_at: "2026-06-30T00:00:01Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const details = await vi.waitFor(() => host.querySelector<HTMLDetailsElement>("details.agent-thinking-details"));
    expect(details).not.toBeNull();
    expect(details?.open).toBe(false);
  });

  it("maps webAgentUI.thinkingMode hidden to the legacy hidden behavior", async () => {
    vi.mocked(getGlobalSettings).mockResolvedValue({ path: "", exists: true, doc: { webAgentUI: { thinkingMode: "hidden", showThinking: true } }, masked: [] });
    vi.mocked(listAgentTasks).mockResolvedValue([{
      id: 160,
      agent_name: "web-agent",
      description: "Hidden mode",
      status: "completed",
      metadata_json: "{\"cwd\":\"/repo\"}",
      result_json: "{\"response\":\"final answer\"}"
    }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 160, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"work\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 160, event_type: "thinking_delta", payload_json: "{\"content\":\"private reasoning\"}", created_at: "2026-06-30T00:00:01Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const conversation = await vi.waitFor(() => host.querySelector(".agent-conversation-scroll"));
    expect(conversation?.querySelector("details.agent-thinking-details")).toBeNull();
    expect(conversation?.textContent).not.toContain("private reasoning");
  });

  it("groups contiguous thinking deltas into one phase and starts a new phase after assistant text", async () => {
    vi.mocked(getGlobalSettings).mockResolvedValue({ path: "", exists: true, doc: { webAgentUI: { thinkingMode: "summary" } }, masked: [] });
    vi.mocked(listAgentTasks).mockResolvedValue([{
      id: 161,
      agent_name: "web-agent",
      description: "Thinking phases",
      status: "completed",
      metadata_json: "{\"cwd\":\"/repo\"}",
      result_json: "{\"response\":\"final answer\"}"
    }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 161, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"work\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 161, event_type: "thinking_delta", payload_json: "{\"content\":\"phase one part one\\n\"}", created_at: "2026-06-30T00:00:01Z" },
      { id: 3, task_id: 161, event_type: "thinking_delta", payload_json: "{\"content\":\"phase one part two\"}", created_at: "2026-06-30T00:00:02Z" },
      { id: 4, task_id: 161, event_type: "text_delta", payload_json: "{\"content\":\"visible answer\"}", created_at: "2026-06-30T00:00:03Z" },
      { id: 5, task_id: 161, event_type: "thinking_delta", payload_json: "{\"content\":\"phase two\"}", created_at: "2026-06-30T00:00:04Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelectorAll("details.agent-thinking-details")).toHaveLength(2));
    const summaries = Array.from(host.querySelectorAll("details.agent-thinking-details summary")).map((node) => node.textContent || "");
    expect(summaries[0]).toContain("turn 1");
    expect(summaries[0]).toContain("phase 1");
    expect(summaries[0]).toContain("2 lines");
    expect(summaries[0]).toContain("tokens unavailable");
    expect(summaries[1]).toContain("phase 2");
    expect(host.textContent).toContain("visible answer");
  });

  it("keeps expanded state scoped to the selected thinking phase when the mode rerenders", async () => {
    vi.mocked(getGlobalSettings).mockResolvedValue({ path: "", exists: true, doc: { webAgentUI: { thinkingMode: "summary" } }, masked: [] });
    vi.mocked(listAgentTasks).mockResolvedValue([{
      id: 162,
      agent_name: "web-agent",
      description: "Thinking state",
      status: "completed",
      metadata_json: "{\"cwd\":\"/repo\"}",
      result_json: "{\"response\":\"final answer\"}"
    }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 162, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"work\"}", created_at: "2026-06-30T00:00:00Z" },
      { id: 2, task_id: 162, event_type: "thinking_delta", payload_json: "{\"content\":\"private reasoning\"}", created_at: "2026-06-30T00:00:01Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });
    const details = await vi.waitFor(() => host.querySelector<HTMLDetailsElement>("details.agent-thinking-details"));
    await act(async () => details?.querySelector("summary")?.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(details?.open).toBe(true);

    const selector = host.querySelector<HTMLSelectElement>('select[aria-label="Thinking"]');
    expect(selector).not.toBeNull();
    await act(async () => {
      if (selector) {
        selector.value = "hidden";
        selector.dispatchEvent(new Event("change", { bubbles: true }));
      }
    });
    await act(async () => {
      if (selector) {
        selector.value = "summary";
        selector.dispatchEvent(new Event("change", { bubbles: true }));
      }
    });
    const restored = await vi.waitFor(() => host.querySelector<HTMLDetailsElement>("details.agent-thinking-details"));
    expect(restored?.open).toBe(true);
  });

  it("hides the thinking placeholder when webAgentUI.showThinking is false", async () => {
    vi.mocked(getGlobalSettings).mockResolvedValue({ path: "", exists: true, doc: { webAgentUI: { showThinking: false } }, masked: [] });
    vi.mocked(listAgentTasks).mockResolvedValue([{
      id: 158,
      agent_name: "web-agent",
      description: "Running without placeholder",
      status: "running",
      metadata_json: "{\"cwd\":\"/repo\"}"
    }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 158, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"work\"}", created_at: "2026-06-30T00:00:00Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-message.user")?.textContent).toContain("work"));
    expect(host.querySelector(".agent-message.assistant.thinking")).toBeNull();
  });

  it("shows full usage fields from agent task usage events", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{
      id: 58,
      agent_name: "web-agent",
      description: "Usage full",
      status: "completed",
      metadata_json: "{\"cwd\":\"/repo\"}",
      result_json: "{\"initial_messages\":4,\"duration_ms\":1234}"
    }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      {
        id: 1,
        task_id: 58,
        event_type: "usage",
        payload_json: "{\"input_tokens\":100,\"output_tokens\":20,\"total_tokens\":120,\"cache_creation_input_tokens\":30,\"cache_read_input_tokens\":70,\"cache_creation_ephemeral_5m_input_tokens\":11,\"cache_creation_ephemeral_1h_input_tokens\":13,\"service_tier\":\"standard\",\"inference_geo\":\"us\",\"speed\":\"fast\",\"context_length\":1000}",
        created_at: "2026-06-30T00:00:00Z"
      },
      { id: 2, task_id: 58, event_type: "message_stop", payload_json: "{\"stop_reason\":\"end_turn\"}", created_at: "2026-06-30T00:00:01Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await act(async () => {
      host.querySelector<HTMLButtonElement>("[title='Toggle right panel']")?.click();
    });
    const usageTab = await vi.waitFor(() => Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("Usage")) as HTMLButtonElement);
    await act(async () => {
      usageTab.click();
    });

    await vi.waitFor(() => expect(host.textContent).toContain("Cache create30"));
    expect(host.textContent).toContain("Cache read70");
    expect(host.textContent).toContain("Cache 5m/1h11/13");
    expect(host.textContent).toContain("Service tierstandard");
    expect(host.textContent).toContain("Inference geous");
    expect(host.textContent).toContain("Speedfast");
    expect(host.textContent).toContain("Initial messages4");
    expect(host.textContent).toContain("Stop reasonend_turn");
  });

  it("resolves pending permission requests through the API", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 59, agent_name: "web-agent", description: "Permission", status: "running", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(getAgentTask).mockResolvedValue({ id: 59, agent_name: "web-agent", description: "Permission", status: "running", metadata_json: "{\"cwd\":\"/repo\"}" });
    vi.mocked(listAgentTaskEvents).mockResolvedValue([
      { id: 1, task_id: 59, event_type: "permission_request", payload_json: "{\"request_id\":\"perm-59\",\"tool_name\":\"Bash\",\"reason\":\"run test\",\"input\":\"{\\\"command\\\":\\\"go test ./...\\\"}\"}", created_at: "2026-06-30T00:00:00Z" }
    ]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await act(async () => {
      host.querySelector<HTMLButtonElement>("[title='Toggle right panel']")?.click();
    });
    const permissionsTab = await vi.waitFor(() => Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("Permissions")) as HTMLButtonElement);
    await act(async () => {
      permissionsTab.click();
    });
    const approve = await vi.waitFor(() => Array.from(host.querySelectorAll("button")).find((button) => button.textContent === "Approve") as HTMLButtonElement);
    await act(async () => {
      approve.click();
    });

    await vi.waitFor(() => expect(resolveAgentTaskPermission).toHaveBeenCalledWith(identity, 59, "perm-59", expect.objectContaining({ allowed: true })));
  });

  it("renders parent and continuation task events as one conversation", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([
      { id: 52, agent_name: "web-agent", description: "Chain", status: "completed", metadata_json: "{\"cwd\":\"/repo\",\"continuation_of_task_id\":51}" },
      { id: 51, agent_name: "web-agent", description: "Chain", status: "completed", metadata_json: "{\"cwd\":\"/repo\"}" }
    ]);
    vi.mocked(listAgentTaskEvents).mockImplementation(async (_identity, taskID) => {
      if (taskID === 51) {
        return [
          { id: 1, task_id: 51, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"first prompt\"}", created_at: "2026-06-30T00:00:00Z" },
          { id: 2, task_id: 51, event_type: "text_delta", payload_json: "{\"content\":\"first reply\"}", created_at: "2026-06-30T00:00:01Z" }
        ];
      }
      return [
        { id: 3, task_id: 52, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"second prompt\"}", created_at: "2026-06-30T00:00:02Z" },
        { id: 4, task_id: 52, event_type: "text_delta", payload_json: "{\"content\":\"second reply\"}", created_at: "2026-06-30T00:00:03Z" }
      ];
    });

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.textContent).toContain("first prompt"));
    expect(host.textContent).toContain("first reply");
    expect(host.textContent).toContain("second prompt");
    expect(host.textContent).toContain("second reply");
  });

  it("sends the current composer message", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 45, agent_name: "web-agent", description: "Scroll", status: "ready", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([]);
    vi.mocked(streamAgentTaskEvents).mockImplementation(async (_identity, _taskId, callbacks) => {
      callbacks.onEvent({ type: "agent_task_event", event: { id: 1, task_id: 45, event_type: "message", payload_json: "{\"from_agent\":\"webui\",\"content\":\"scroll check\"}", created_at: "2026-06-30T00:00:00Z" } });
      callbacks.onDone();
    });
    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const textarea = await vi.waitFor(() => host.querySelector("textarea") as HTMLTextAreaElement);
    await act(async () => {
      setTextareaValue(textarea, "scroll check");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });

    await vi.waitFor(() => expect(sendAgentTaskMessage).toHaveBeenCalled());
    await vi.waitFor(() => expect(getAgentTask).toHaveBeenCalledWith(identity, 45));
    await vi.waitFor(() => expect(host.querySelector(".composer-state")?.textContent).toContain("ready"));
  });

  it("pastes an image into the composer and sends an inline attachment", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 46, agent_name: "web-agent", description: "Image input", status: "ready", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([]);
    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const textarea = await vi.waitFor(() => host.querySelector("textarea[aria-label='Message composer']") as HTMLTextAreaElement);
    const image = new File(["png-bytes"], "clip.png", { type: "image/png" });
    const clipboardData = {
      items: [{ kind: "file", type: "image/png", getAsFile: () => image }]
    } as unknown as DataTransfer;
    const paste = new Event("paste", { bubbles: true, cancelable: true });
    Object.defineProperty(paste, "clipboardData", { value: clipboardData });
    await act(async () => textarea.dispatchEvent(paste));

    expect(host.querySelector(".agent-image-attachment img")).not.toBeNull();
    expect(host.querySelector(".agent-image-attachment")?.textContent).toContain("clip.png");
    await act(async () => {
      setTextareaValue(textarea, "describe this image");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => host.querySelector<HTMLButtonElement>("button[aria-label='Send']")?.click());

    await vi.waitFor(() => expect(sendAgentTaskMessage).toHaveBeenCalled());
    const request = vi.mocked(sendAgentTaskMessage).mock.calls[0][2];
    expect(request.attachments).toHaveLength(1);
    expect(request.attachments?.[0]).toMatchObject({ type: "image", media_type: "image/png", name: "clip.png", size_bytes: image.size });
    expect(request.attachments?.[0].inline_data).toBeTruthy();
  });

  it("shows pending user bubble and thinking placeholder while a send is in flight", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 55, agent_name: "web-agent", description: "Pending", status: "ready", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([]);
    vi.mocked(streamAgentTaskEvents).mockImplementation(async (_identity, _taskId, callbacks) => {
      callbacks.onEvent({ type: "connected" });
    });
    let resolveSend: (() => void) | null = null;
    vi.mocked(sendAgentTaskMessage).mockReturnValue(new Promise((resolve) => {
      resolveSend = () => resolve(100);
    }));

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const textarea = await vi.waitFor(() => host.querySelector("textarea") as HTMLTextAreaElement);
    await act(async () => {
      setTextareaValue(textarea, "make the UI calmer");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });

    await vi.waitFor(() => expect(host.querySelector(".agent-message.user.status-sending")?.textContent).toContain("make the UI calmer"));
    expect(host.querySelector(".agent-message.assistant.thinking")?.textContent).toContain("Sending");
    expect(host.querySelector(".agent-message.assistant.thinking")?.textContent).toContain("Events live");

    await act(async () => {
      resolveSend?.();
    });
  });

  it("keeps composer running only when backend task is still running after send", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 54, agent_name: "web-agent", description: "Running", status: "ready", metadata_json: "{\"cwd\":\"/repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([]);
    vi.mocked(getAgentTask).mockResolvedValue({ id: 54, agent_name: "web-agent", description: "Running", status: "running", metadata_json: "{\"cwd\":\"/repo\"}" });
    vi.mocked(streamAgentTaskEvents).mockImplementation(async (_identity, _taskId, callbacks) => {
      callbacks.onEvent({ type: "connected" });
    });

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const textarea = await vi.waitFor(() => host.querySelector("textarea") as HTMLTextAreaElement);
    await act(async () => {
      setTextareaValue(textarea, "long task");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });

    await vi.waitFor(() => expect(host.querySelector(".composer-state")?.textContent).toContain("running"));
    expect(host.querySelector(".agent-send-button.danger")?.getAttribute("aria-label")).toContain("Cancel");
  });

  it("creates a continuation task instead of sending to a terminal task", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([{ id: 46, agent_name: "web-agent", description: "Done", status: "completed", model: "gpt-5.5", metadata_json: "{\"cwd\":\"/repo\",\"workspace_name\":\"repo\"}" }]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([]);
    vi.mocked(createAgentTask).mockResolvedValue(47);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    const textarea = await vi.waitFor(() => host.querySelector("textarea") as HTMLTextAreaElement);
    await act(async () => {
      setTextareaValue(textarea, "continue");
      textarea.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    });

    await vi.waitFor(() => expect(createAgentTask).toHaveBeenCalledWith(identity, expect.objectContaining({ status: "running", prompt: "continue" })));
    await vi.waitFor(() => expect(sendAgentTaskMessage).toHaveBeenCalledWith(identity, 47, expect.objectContaining({ from_agent: "webui", content: "continue" })));
  });

  it("automatically validates workspace input and removes the manual validate button", async () => {
    vi.useFakeTimers();
    vi.mocked(listAgentTasks).mockResolvedValue([]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([]);
    vi.mocked(getStatus).mockResolvedValue({ workspace: "" });
    vi.mocked(validateAgentWorkspace).mockResolvedValue({
      cwd: "/repo/normalized",
      workspace_name: "normalized",
      exists: true,
      is_dir: true,
      is_git_repo: true
    });

    try {
      await act(async () => {
        root.render(
          <I18nProvider>
            <WebAgentPage identity={identity} onStatus={vi.fn()} />
          </I18nProvider>
        );
      });
      await vi.waitFor(() => expect(host.textContent).toContain("New Session"));
      await act(async () => {
        (Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("New Session")) as HTMLButtonElement).click();
      });

      expect(Array.from(host.querySelectorAll(".agent-modal button")).some((button) => button.textContent?.includes("Validate"))).toBe(false);
      const input = await vi.waitFor(() => host.querySelector(".agent-modal input") as HTMLInputElement);
      await act(async () => {
        const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")?.set;
        setter?.call(input, "/repo/raw");
        input.dispatchEvent(new Event("input", { bubbles: true }));
      });
      await act(async () => {
        vi.advanceTimersByTime(450);
      });

      await vi.waitFor(() => expect(validateAgentWorkspace).toHaveBeenCalledWith(identity, "/repo/raw"));
      await vi.waitFor(() => expect(host.textContent).toContain("Ready: /repo/normalized"));
    } finally {
      vi.useRealTimers();
    }
  });

  it("blocks new session creation when workspace validation fails", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([]);
    vi.mocked(getStatus).mockResolvedValue({ workspace: "" });
    vi.mocked(validateAgentWorkspace).mockRejectedValue(new Error("path must be absolute"));

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });
    await vi.waitFor(() => expect(host.textContent).toContain("New Session"));
    await act(async () => {
      (Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("New Session")) as HTMLButtonElement).click();
    });
    const input = await vi.waitFor(() => host.querySelector(".agent-modal input") as HTMLInputElement);
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")?.set;
      setter?.call(input, "relative/path");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      (Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("Create Session")) as HTMLButtonElement).click();
    });

    await vi.waitFor(() => expect(host.textContent).toContain("path must be absolute"));
    expect(createTenantSession).not.toHaveBeenCalled();
    expect(createAgentTask).not.toHaveBeenCalled();
  });

  it("creates new sessions with the normalized workspace returned by validation", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([]);
    vi.mocked(getStatus).mockResolvedValue({ workspace: "" });
    vi.mocked(validateAgentWorkspace).mockResolvedValue({
      cwd: "/repo/normalized",
      workspace_name: "normalized",
      exists: true,
      is_dir: true,
      is_git_repo: true
    });
    vi.mocked(createAgentTask).mockResolvedValue(88);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });
    await vi.waitFor(() => expect(host.textContent).toContain("New Session"));
    await act(async () => {
      (Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("New Session")) as HTMLButtonElement).click();
    });
    const input = await vi.waitFor(() => host.querySelector(".agent-modal input") as HTMLInputElement);
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")?.set;
      setter?.call(input, "/repo/raw");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      (Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("Create Session")) as HTMLButtonElement).click();
    });

    await vi.waitFor(() => expect(createTenantSession).toHaveBeenCalledWith(identity, expect.objectContaining({
      cwd: "/repo/normalized",
      metadata_json: expect.stringContaining("\"cwd\":\"/repo/normalized\"")
    })));
    expect(createAgentTask).toHaveBeenCalledWith(identity, expect.objectContaining({
      metadata_json: expect.objectContaining({
        cwd: "/repo/normalized",
        workspace_name: "normalized"
      })
    }));
  });

  it("stores code prompt mode by default in new session metadata", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([]);
    vi.mocked(createAgentTask).mockResolvedValue(88);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.textContent).toContain("New Session"));
    await act(async () => {
      (Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("New Session")) as HTMLButtonElement).click();
    });
    const modeGroup = await vi.waitFor(() => host.querySelector('[aria-label="New session prompt mode"]') as HTMLElement);
    expect(modeGroup.querySelector("button.active")?.textContent).toContain("Code");
    await act(async () => {
      (Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("Create Session")) as HTMLButtonElement).click();
    });

    await vi.waitFor(() => expect(createAgentTask).toHaveBeenCalledWith(identity, expect.objectContaining({
      trace_id: expect.stringMatching(/^web-agent-/),
      metadata_json: expect.objectContaining({
        prompt_mode: "code",
        trace_id: expect.stringMatching(/^web-agent-/),
        run_trace_id: expect.stringMatching(/^web-agent-/)
      })
    })));
  });

  it("stores explicit chat prompt mode in new session metadata", async () => {
    vi.mocked(listAgentTasks).mockResolvedValue([]);
    vi.mocked(listAgentTaskEvents).mockResolvedValue([]);
    vi.mocked(createAgentTask).mockResolvedValue(89);

    await act(async () => {
      root.render(
        <I18nProvider>
          <WebAgentPage identity={identity} onStatus={vi.fn()} />
        </I18nProvider>
      );
    });

    await vi.waitFor(() => expect(host.textContent).toContain("New Session"));
    await act(async () => {
      (Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("New Session")) as HTMLButtonElement).click();
    });
    const modeGroup = await vi.waitFor(() => host.querySelector('[aria-label="New session prompt mode"]') as HTMLElement);
    await act(async () => {
      (Array.from(modeGroup.querySelectorAll("button")).find((button) => button.textContent?.includes("Chat")) as HTMLButtonElement).click();
    });
    expect(modeGroup.querySelector("button.active")?.textContent).toContain("Chat");
    await act(async () => {
      (Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("Create Session")) as HTMLButtonElement).click();
    });

    await vi.waitFor(() => expect(createAgentTask).toHaveBeenCalledWith(identity, expect.objectContaining({
      metadata_json: expect.objectContaining({
        prompt_mode: "chat"
      })
    })));
  });

});

describe("StreamingAssistantContent", () => {
  it("finishes typing the complete response after the stream closes", async () => {
    vi.useFakeTimers();
    const host = document.createElement("div");
    const root = createRoot(host);
    const partial = "First paragraph is still arriving.";
    const complete = `${partial} Final complete response.`;

    try {
      await act(async () => {
        root.render(<StreamingAssistantContent content={partial} animate live final={false} />);
      });
      await act(async () => {
        vi.advanceTimersByTime(50);
      });
      expect(host.textContent).not.toBe(partial);

      await act(async () => {
        root.render(<StreamingAssistantContent content={complete} animate live={false} final />);
      });
      expect(host.textContent).not.toContain("complete");

      await act(async () => {
        vi.advanceTimersByTime(2_000);
      });
      expect(host.textContent).toBe(complete);
    } finally {
      await act(async () => root.unmount());
      vi.useRealTimers();
    }
  });

  it("shows the complete response immediately when reduced motion is enabled", async () => {
    const originalMatchMedia = window.matchMedia;
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      value: vi.fn().mockReturnValue({
        matches: true,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn()
      })
    });
    const host = document.createElement("div");
    const root = createRoot(host);

    try {
      await act(async () => {
        root.render(<StreamingAssistantContent content="Reduced motion response" animate live final={false} />);
      });
      expect(host.textContent).toContain("Reduced motion response");
    } finally {
      await act(async () => root.unmount());
      Object.defineProperty(window, "matchMedia", { configurable: true, value: originalMatchMedia });
    }
  });
});

function setTextareaValue(textarea: HTMLTextAreaElement, value: string) {
  const descriptor = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value");
  descriptor?.set?.call(textarea, value);
}
