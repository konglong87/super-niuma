import { act, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TeamRunObservability, type TeamRunObservation } from "./TeamRunObservability";

const observation: TeamRunObservation = {
  run: {
    id: "run-observe-42",
    status: "completed",
    coordinator_member_key: "coord",
    member_count: 3,
    created_at: "2026-10-10T01:00:00Z",
    started_at: "2026-10-10T01:00:00Z",
    finished_at: "2026-10-10T01:05:00Z"
  },
  snapshot: {
    team_key: "release-team",
    team_version: 7,
    display_name: "Release Team",
    coordinator_member: "coord",
    mode: "parallel_review",
    source: { provider: "feishu", account_key: "ops-bot", external_chat_id: "chat-88", external_thread_id: "thread-9" },
    members: [
      { member_key: "coord", profile_key: "release-coordinator", profile_version: 4, role: "coordinator", profile_id: 401 },
      { member_key: "reviewer", profile_key: "risk-reviewer", profile_version: 3, role: "reviewer", profile_id: 402 },
      { member_key: "writer", profile_key: "release-writer", profile_version: 2, role: "writer", profile_id: 403 }
    ]
  },
  events: [
    { id: 1, sequence_no: 1, event_type: "delegated", member_key: "coord", from_member_key: "coord", to_member_key: "reviewer", status: "completed", summary: "Review the release notes", created_at: "2026-10-10T01:01:00Z" },
    { id: 2, sequence_no: 2, event_type: "review_started", member_key: "reviewer", from_member_key: "reviewer", to_member_key: "coord", status: "completed", summary: "Reviewer accepted the task", created_at: "2026-10-10T01:02:00Z" },
    { id: 3, sequence_no: 3, event_type: "draft_updated", member_key: "writer", status: "running", payload_json: JSON.stringify({ artifacts: [{ kind: "file", ref: "notes/release.md", action: "updated" }], evidence: ["evidence/review-42.json"] }), created_at: "2026-10-10T01:03:00Z" },
    { id: 4, sequence_no: 4, event_type: "review_complete", member_key: "reviewer", status: "completed", payload_json: JSON.stringify({ artifacts: [{ kind: "artifact", ref: "artifact://review-42", action: "created" }] }), created_at: "2026-10-10T01:04:00Z" }
  ],
  mailbox: [{ id: 9, sequence_no: 1, from_member_key: "reviewer", to_member_key: "coord", message_kind: "review_result", evidence_ref: "evidence/review-42.json", status: "delivered", created_at: "2026-10-10T01:04:00Z" }],
  next_cursor: 0,
  has_more: true
};

function render(element: ReactElement): { host: HTMLDivElement; root: Root } {
  const host = document.createElement("div");
  document.body.replaceChildren(host);
  const root = createRoot(host);
  act(() => root.render(element));
  return { host, root };
}

describe("TeamRunObservability", () => {
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  });

  afterEach(() => {
    document.body.replaceChildren();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  it("renders a real-shaped pinned snapshot with coordinator/reviewer direction and exact profile versions", () => {
    const onProfile = vi.fn();
    const loadMore = vi.fn();
    const { host, root } = render(<TeamRunObservability language="en" observation={observation} onRefresh={vi.fn()} onLoadMore={loadMore} onProfile={onProfile} />);

    expect(host.textContent).toContain("Release Team");
    expect(host.textContent).toContain("@v7");
    expect(host.textContent).toContain("parallel_review");
    expect(host.textContent).toContain("Feishu");
    expect(host.textContent).toContain("release-coordinator@v4");
    expect(host.textContent).toContain("risk-reviewer@v3");
    expect(host.textContent).toContain("Coordinator");
    expect(host.textContent).toContain("Reviewer");
    expect(host.textContent).toContain("coord");
    expect(host.textContent).toContain("reviewer");

    const profileButton = host.querySelector<HTMLButtonElement>(".teamrun-observability__member-button");
    act(() => profileButton?.click());
    expect(onProfile).toHaveBeenCalledWith("release-coordinator", 4);

    const loadMoreButton = host.querySelector<HTMLButtonElement>(".teamrun-observability__load-more button");
    act(() => loadMoreButton?.click());
    expect(loadMore).toHaveBeenCalledTimes(1);
    root.unmount();
  });

  it("keeps live member state event-derived instead of marking every member completed", () => {
    const { host, root } = render(<TeamRunObservability language="en" observation={observation} onRefresh={vi.fn()} onLoadMore={vi.fn()} />);
    const writer = Array.from(host.querySelectorAll<HTMLElement>(".teamrun-observability__member")).find((node) => node.textContent?.includes("writer"));

    expect(host.querySelector(".teamrun-observability__member--running")).not.toBeNull();
    expect(writer?.textContent).toContain("Running");
    expect(writer?.textContent).not.toContain("Completed");
    root.unmount();
  });

  it("makes a missing historical snapshot explicit without inferring topology or source", () => {
    const withoutSnapshot: TeamRunObservation = { ...observation, snapshot: undefined };
    const { host, root } = render(<TeamRunObservability language="zh" observation={withoutSnapshot} onRefresh={vi.fn()} onLoadMore={vi.fn()} />);

    expect(host.textContent).toContain("固定快照不可用");
    expect(host.textContent).toContain("未携带可重建的 Team 快照");
    expect(host.textContent).not.toContain("飞书");
    expect(host.querySelectorAll(".teamrun-observability__member")).toHaveLength(0);
    root.unmount();
  });

  it("renders malicious artifact references as escaped plain text, never inferred links or markup", () => {
    const malicious: TeamRunObservation = {
      ...observation,
      events: [{ ...observation.events[2], payload_json: JSON.stringify({ artifacts: [{ kind: "file", ref: '<img src=x onerror="alert(1)">', action: "created" }] }) }]
    };
    const { host, root } = render(<TeamRunObservability language="en" observation={malicious} onRefresh={vi.fn()} onLoadMore={vi.fn()} />);

    expect(host.textContent).toContain('<img src=x onerror="alert(1)">');
    expect(host.querySelector("img")).toBeNull();
    expect(host.querySelector("a")).toBeNull();
    root.unmount();
  });
});
