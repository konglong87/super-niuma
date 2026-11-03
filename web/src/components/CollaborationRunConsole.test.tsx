import { act, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TeamRunConsole, TeamRunReplayConsole } from "./CollaborationRunConsole";
import type { AgentProfileRecord, AgentTeamMember, AgentTeamRecord, AgentTeamRun } from "../lib/types";

const team: AgentTeamRecord = {
  id: 7,
  team_key: "release-team",
  team_version: 3,
  display_name: "Release Team",
  description: "Ship a release safely",
  status: "published",
  schema_version: 1,
  policy_json: "{}"
};

const profiles: AgentProfileRecord[] = [
  { id: 11, profile_key: "copywriter", profile_version: 2, display_name: "Copywriter", status: "published", scope: "tenant_shared", config_json: "{}" },
  { id: 12, profile_key: "coder", profile_version: 1, display_name: "Coder", status: "published", scope: "tenant_shared", config_json: "{}" }
];

const members: AgentTeamMember[] = [
  { id: 21, team_id: 7, member_key: "copywriter", profile_id: 11, role: "writer", status: "active" },
  { id: 22, team_id: 7, member_key: "coder", profile_id: 12, role: "coder", status: "active" }
];

const runs: AgentTeamRun[] = [
  { id: "run-active", team_id: 7, status: "running", coordinator_member_key: "coordinator", member_count: 2, used_tokens: 420, used_turns: 3, created_at: "2026-10-10T01:00:00Z", started_at: "2026-10-10T01:00:00Z" },
  { id: "run-done", team_id: 7, status: "completed", coordinator_member_key: "coordinator", member_count: 2, used_tokens: 900, used_turns: 5, created_at: "2026-10-09T01:00:00Z", started_at: "2026-10-09T01:00:00Z", finished_at: "2026-10-09T01:04:00Z", result_json: "Final release summary" }
];

function render(element: ReactElement): { host: HTMLDivElement; root: Root } {
  const host = document.createElement("div");
  document.body.replaceChildren(host);
  const root = createRoot(host);
  act(() => root.render(element));
  return { host, root };
}

describe("CollaborationRunConsole", () => {
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  });
  afterEach(() => {
    document.body.replaceChildren();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  it("shows a readable run overview and filters without hiding the team identity", () => {
    const inspect = vi.fn();
    const { host, root } = render(<TeamRunConsole language="zh" team={team} members={members} profiles={profiles} runs={runs} onInspect={inspect} onCancel={vi.fn()} />);

    expect(host.textContent).toContain("TeamRun 运行监控");
    expect(host.textContent).toContain("Release Team");
    expect(host.textContent).toContain("run-active");
    expect(host.textContent).toContain("run-done");

    const active = Array.from(host.querySelectorAll<HTMLButtonElement>("[role=tab]")).find((button) => button.textContent?.includes("进行中"));
    act(() => active?.click());
    expect(host.textContent).toContain("run-active");
    expect(host.textContent).not.toContain("run-done");

    const runButton = host.querySelector<HTMLButtonElement>(".collaboration-run-card-main");
    act(() => runButton?.click());
    expect(inspect).toHaveBeenCalledWith(runs[0]);
    root.unmount();
  });

  it("renders pinned profile versions and keeps raw evidence secondary", () => {
    const { host, root } = render(<TeamRunReplayConsole language="en" team={team} members={members} profiles={profiles} run={runs[1]} validation={{ valid: true }} onBack={vi.fn()} />);

    expect(host.textContent).toContain("Team topology");
    expect(host.textContent).toContain("Copywriter");
    expect(host.textContent).toContain("copywriter@v2");
    expect(host.textContent).toContain("Coder");
    expect(host.textContent).toContain("Final release summary");
    expect(host.querySelector("details.collaboration-raw-details")).not.toBeNull();
    root.unmount();
  });
});
