import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { GoalWorkbench } from "./GoalWorkbench";
import { I18nProvider } from "../lib/i18n";
import type { GoalEvidence, GoalPlan, GoalRecord, IdentityConfig } from "../lib/types";
import {
  createGoal,
  getGoalPlan,
  listGoalEvidence,
  listGoalEvents,
  listGoals,
  resumeGoal,
  runGoalOnce,
  stopGoal
} from "../lib/api";

vi.mock("../lib/api", () => ({
  createGoal: vi.fn(),
  getGoalPlan: vi.fn(),
  listGoalEvidence: vi.fn(),
  listGoalEvents: vi.fn(),
  listGoals: vi.fn(),
  resumeGoal: vi.fn(),
  runGoalOnce: vi.fn(),
  stopGoal: vi.fn()
}));

const identity: IdentityConfig = {
  apiBase: "/api",
  apiToken: "test-token",
  mobileJwt: "",
  tenantKey: "webui-local",
  userId: "webui-local-user",
  deviceId: "test-device",
  role: "owner",
  model: "deepseek-v4-flash"
};

describe("GoalWorkbench", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    vi.mocked(createGoal).mockReset().mockResolvedValue({ id: "goal_new", status: "active" });
    vi.mocked(listGoals).mockReset().mockResolvedValue([goalRecord()]);
    vi.mocked(listGoalEvents).mockReset().mockResolvedValue([
      { id: "evt_1", goal_id: "goal_1", type: "turn_finished", message: "turn finished", status: "active" }
    ]);
    vi.mocked(getGoalPlan).mockReset().mockResolvedValue(goalPlan());
    vi.mocked(listGoalEvidence).mockReset().mockResolvedValue([capabilityEvidence()]);
    vi.mocked(resumeGoal).mockReset().mockResolvedValue(goalRecord());
    vi.mocked(runGoalOnce).mockReset().mockResolvedValue({ goal: goalRecord() });
    vi.mocked(stopGoal).mockReset().mockResolvedValue({ ...goalRecord(), status: "stopped" });
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => {
      root.unmount();
    });
  });

  it("renders selected goal plan, criteria, evidence, and capability loop risk", async () => {
    await act(async () => {
      root.render(
        <I18nProvider>
          <GoalWorkbench identity={identity} selectedSessionId={41} onStatus={vi.fn()} onDataChanged={vi.fn()} />
        </I18nProvider>
      );
      await flushPromises();
    });

    expect(host.textContent).toContain("Validate agent capability loop closure");
    expect(host.textContent).toContain("Verify parent reuse");
    expect(host.textContent).toContain("1/2");
    expect(host.textContent).toContain("TaskStore failed partial evidence");
    expect(host.textContent).toContain("source: task_store");
    expect(host.textContent).toContain("status: failed");
    expect(host.textContent).toContain("partial");
    expect(host.textContent).toContain("evidence: sub-agent found missing verification");
    expect(host.textContent).toContain("risk: parent may continue without proof");
    expect(host.textContent).toContain("next: rerun acceptance script");
    expect(vi.mocked(getGoalPlan)).toHaveBeenCalledWith(identity, "goal_1");
    expect(vi.mocked(listGoalEvidence)).toHaveBeenCalledWith(identity, "goal_1", 20);
  });

  it("skips placeholder capability loop fields in goal evidence", async () => {
    vi.mocked(listGoalEvidence).mockResolvedValue([placeholderCapabilityEvidence()]);

    await act(async () => {
      root.render(
        <I18nProvider>
          <GoalWorkbench identity={identity} selectedSessionId={41} onStatus={vi.fn()} onDataChanged={vi.fn()} />
        </I18nProvider>
      );
      await flushPromises();
    });

    expect(host.textContent).toContain("source: agent_get");
    expect(host.textContent).toContain("evidence: concrete goal evidence");
    expect(host.textContent).toContain("next: inspect goal prompt dump");
    expect(host.textContent).not.toContain("None observed");
    expect(host.textContent).not.toContain("unknown: None observed");
    expect(host.textContent).not.toContain("risk: None observed");
    expect(host.textContent).not.toContain("verification: None observed");
  });
});

function goalRecord(): GoalRecord {
  return {
    id: "goal_1",
    objective: "Validate agent capability loop closure",
    status: "active",
    turns_used: 2,
    turn_budget: 5,
    token_budget: 20000,
    last_next_action: "rerun acceptance script",
    last_reason: "waiting on evidence"
  };
}

function goalPlan(): GoalPlan {
  return {
    goal_id: "goal_1",
    current_step_id: "step_reuse",
    summary: "Close the evidence loop before claiming success.",
    acceptance_criteria: [
      { id: "crit_1", description: "Evidence exists", required: true, status: "passed" },
      { id: "crit_2", description: "Parent request reuses evidence", required: true, status: "pending" }
    ],
    steps: [
      { id: "step_reuse", title: "Verify parent reuse", status: "active", rationale: "The parent must see sub-agent evidence." }
    ],
    risks: [
      { id: "risk_1", description: "Parent may continue without proof", severity: "high", status: "open" }
    ]
  };
}

function capabilityEvidence(): GoalEvidence {
  return {
    id: "ev_agent_get",
    goal_id: "goal_1",
    type: "manual",
    summary: "TaskStore failed partial evidence",
    passed: false,
    payload: {
      evidence_source: "terminal_agent_task_store",
      agent_status: "failed",
      partial_evidence: true,
      capability_loop: {
        evidence: ["sub-agent found missing verification"],
        risks: ["parent may continue without proof"],
        unknowns: ["whether the next turn includes it"],
        verification: ["prompt dump grep"],
        next_action: "rerun acceptance script"
      }
    } as unknown as number[]
  };
}

function placeholderCapabilityEvidence(): GoalEvidence {
  return {
    id: "ev_agent_get_placeholder",
    goal_id: "goal_1",
    type: "manual",
    summary: "AgentGet capability evidence",
    passed: true,
    payload: {
      evidence_source: "agent_get",
      agent_status: "completed",
      partial_evidence: false,
      capability_loop: {
        evidence: ["None observed", "concrete goal evidence"],
        risks: ["None observed"],
        unknowns: ["None observed"],
        verification: ["None observed"],
        next_action: "inspect goal prompt dump"
      }
    } as unknown as number[]
  };
}

function flushPromises(): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, 0));
}
