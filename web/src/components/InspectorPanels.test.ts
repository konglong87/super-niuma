import { describe, expect, it, vi } from "vitest";
import { downloadBlob, filterLocalTraceSessions, formatLoopInterval, inferCurrentLocalProject, isLocalTraceTestSession, latestTraceRecap, loopMeta, runRefreshTask, settledErrorSummary, settledValue, summarizeSkillTelemetry, traceQualityMetrics, traceRecaps } from "./InspectorPanels";
import type { TelemetryRecord, TraceDetail, TraceQuality, TraceSessionSummary } from "../lib/types";

const sessions: TraceSessionSummary[] = [
  {
    source: "local",
    session_id: "golang-new",
    title: "Fix Local Trace",
    cwd: "Users-example-GolandProjects-niuma",
    updated_at: "2026-06-24T10:00:00Z"
  },
  {
    source: "local",
    session_id: "huyu-old",
    title: "Card logic",
    cwd: "Users-example-GolandProjects-huyu",
    updated_at: "2026-06-20T10:00:00Z"
  },
  {
    source: "local",
    session_id: "golang-older",
    message_hint: "remember examples",
    path: "/Users/example/.claude/projects/Users-example-GolandProjects-niuma/golang-older.jsonl",
    updated_at: "2026-06-23T10:00:00Z"
  },
  {
    source: "local",
    session_id: "runtime-test",
    title: "do it",
    cwd: "var-folders-dg-2yb1ss652wsd64k9nltz3fcw0000gn-T-TestRuntimeRunsSubagentStopHook2007402057-001",
    updated_at: "2026-06-25T10:00:00Z"
  }
];

describe("local trace filters", () => {
  it("infers the current project from the latest local session", () => {
    expect(inferCurrentLocalProject(sessions)).toBe("Users-example-GolandProjects-niuma");
  });

  it("filters by current project and search text", () => {
    const result = filterLocalTraceSessions(sessions, {
      search: "remember",
      timeFilter: "all",
      projectOnly: true,
      currentProject: "Users-example-GolandProjects-niuma",
      now: new Date("2026-06-24T12:00:00Z")
    });

    expect(result.map((item) => item.session_id)).toEqual(["golang-older"]);
  });

  it("filters by relative time window", () => {
    const result = filterLocalTraceSessions(sessions, {
      search: "",
      timeFilter: "24h",
      projectOnly: false,
      currentProject: "",
      now: new Date("2026-06-24T12:00:00Z")
    });

    expect(result.map((item) => item.session_id)).toEqual(["golang-new"]);
  });

  it("hides local runtime test sessions by default and includes them behind the switch", () => {
    expect(isLocalTraceTestSession(sessions[3])).toBe(true);
    expect(inferCurrentLocalProject(sessions)).toBe("Users-example-GolandProjects-niuma");

    const hidden = filterLocalTraceSessions(sessions, {
      search: "",
      timeFilter: "all",
      projectOnly: false,
      currentProject: "",
      now: new Date("2026-06-25T12:00:00Z")
    });
    expect(hidden.map((item) => item.session_id)).not.toContain("runtime-test");

    const shown = filterLocalTraceSessions(sessions, {
      search: "",
      timeFilter: "all",
      projectOnly: false,
      includeTestSessions: true,
      currentProject: "",
      now: new Date("2026-06-25T12:00:00Z")
    });
    expect(shown.map((item) => item.session_id)).toContain("runtime-test");
  });
});

describe("trace recap helpers", () => {
  it("uses first-class recap fields and skips invalidated latest recap", () => {
    const trace = {
      recaps: [
        { id: "recap-1", content: "继续实现 Web 可观测 recap。", status: "ok", source: "away" },
        { id: "recap-2", content: "Recap invalidated by rewind.", status: "invalidated", source: "rewind" }
      ]
    } as TraceDetail;

    expect(traceRecaps(trace)).toHaveLength(2);
    expect(latestTraceRecap(trace)?.id).toBe("recap-1");
  });

  it("falls back to recap_summary events for older trace responses", () => {
    const trace = {
      events: [
        {
          id: "event-1",
          type: "recap_summary",
          name: "session.recap",
          content: "从事件中读取 recap。",
          model: "recap-model",
          properties: { source: "manual", status: "ok", duration_ms: 12, summarizes_entry_id: "user-1" }
        }
      ]
    } as TraceDetail;

    expect(latestTraceRecap(trace)).toMatchObject({
      id: "event-1",
      content: "从事件中读取 recap。",
      source: "manual",
      status: "ok",
      duration_ms: 12,
      summarizes_entry_id: "user-1"
    });
  });
});

describe("trace quality metrics", () => {
  it("shows final verification and recovered test attempts", () => {
    const quality: TraceQuality = {
      tests_run: true,
      tests_passed: true,
      test_attempts: 3,
      failed_test_attempts: 2,
      test_failure_recovered: true,
      completion_verified: true,
      final_verification_passed: true,
      tool_errors: 2,
      recoveries: 1,
      gate_blocks: 0,
      todo_writes: 0
    };

    expect(traceQualityMetrics(quality, (key) => key)).toEqual([
      { label: "trace.tests", value: "trace.passed" },
      { label: "trace.finalVerification", value: "trace.passed" },
      { label: "trace.testAttempts", value: 3 },
      { label: "trace.failedTestAttempts", value: 2 },
      { label: "trace.testFailureRecovered", value: "trace.yes" },
      { label: "trace.completionVerified", value: "trace.yes" },
      { label: "trace.toolErrors", value: 2 },
      { label: "trace.recoveries", value: 1 },
      { label: "trace.gateBlocks", value: 0 },
      { label: "trace.todoWrites", value: 0 }
    ]);
  });

  it("keeps legacy trace quality readable when final fields are absent", () => {
    const quality: TraceQuality = {
      tests_run: false,
      tests_passed: false,
      completion_verified: true,
      tool_errors: 0,
      recoveries: 0,
      gate_blocks: 0,
      todo_writes: 0
    };

    expect(traceQualityMetrics(quality, (key) => key).slice(0, 5)).toEqual([
      { label: "trace.tests", value: "trace.notRun" },
      { label: "trace.finalVerification", value: "trace.unknown" },
      { label: "trace.testAttempts", value: "-" },
      { label: "trace.failedTestAttempts", value: "-" },
      { label: "trace.testFailureRecovered", value: "trace.unknown" }
    ]);
  });
});

describe("skill telemetry summary", () => {
  it("groups by skill source version and fallback", () => {
    const telemetry: TelemetryRecord[] = [
      {
        id: 1,
        name: "tool.execution.finished",
        status: "ok",
        duration_ms: 12,
        input_tokens: 3,
        output_tokens: 4,
        occurred_at: "2026-06-24T10:00:00Z",
        properties_json: JSON.stringify({ active_skill: "review", active_skill_source: "tenant", active_skill_version: 2, active_skill_fallback: false })
      },
      {
        id: 2,
        name: "tool.execution.finished",
        status: "error",
        duration_ms: 8,
        occurred_at: "2026-06-24T10:01:00Z",
        properties: { active_skill: "review", active_skill_source: "tenant", active_skill_version: 2, active_skill_fallback: false }
      },
      {
        id: 3,
        name: "tool.execution.finished",
        status: "ok",
        duration_ms: 5,
        occurred_at: "2026-06-24T10:02:00Z",
        properties_json: JSON.stringify({ active_skill: "review", active_skill_source: "local", active_skill_version: 1, active_skill_fallback: true })
      }
    ];

    const result = summarizeSkillTelemetry(telemetry);

    expect(result).toHaveLength(2);
    expect(result[0]).toMatchObject({ skill: "review", source: "local", version: "1", fallback: "true", count: 1 });
    expect(result[1]).toMatchObject({ skill: "review", source: "tenant", version: "2", fallback: "false", count: 2, errors: 1, durationMs: 20, tokens: 7 });
  });
});

describe("loop helpers", () => {
  it("formats loop interval and meta", () => {
    expect(formatLoopInterval({ id: "bg_1", interval_seconds: 600 })).toBe("10m");
    expect(formatLoopInterval({ id: "bg_1", interval_seconds: 7200 })).toBe("2h");
    expect(formatLoopInterval({ id: "bg_1", spec: "@every 45s" })).toBe("@every 45s");
    expect(loopMeta({ id: "bg_1", interval_seconds: 600, run_count: 4, last_run_at: "2026-06-24T10:00:00Z" })).toContain("runs 4");
  });
});

describe("refresh helpers", () => {
  it("keeps fulfilled values available when another refresh task fails", async () => {
    const skillsTask = await runRefreshTask({
      label: "tenant skills",
      run: async () => [{ skill_key: "teach-v2", version: 6 }]
    });
    const mobileTask = await runRefreshTask({
      label: "mobile sessions",
      run: async () => {
        throw new Error("mobile jwt secret is not configured");
      }
    });

    expect(settledValue(skillsTask.result, [])).toEqual([{ skill_key: "teach-v2", version: 6 }]);
    expect(settledValue(mobileTask.result, [])).toEqual([]);
    expect(settledErrorSummary([skillsTask, mobileTask])).toContain("mobile sessions: mobile jwt secret is not configured");
  });
});

describe("runtime trace artifact download", () => {
  it("clicks an attached anchor and revokes the object URL on a later task", () => {
    vi.useFakeTimers();
    const createObjectURL = vi.fn(() => "blob:runtime-trace");
    const revokeObjectURL = vi.fn();
    const originalCreateObjectURL = Object.getOwnPropertyDescriptor(URL, "createObjectURL");
    const originalRevokeObjectURL = Object.getOwnPropertyDescriptor(URL, "revokeObjectURL");
    Object.defineProperty(URL, "createObjectURL", { configurable: true, value: createObjectURL });
    Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: revokeObjectURL });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      expect(this.isConnected).toBe(true);
      expect(this.href).toContain("blob:runtime-trace");
      expect(this.download).toBe("runtime-trace-session-a.json");
    });

    try {
      downloadBlob(new Blob(["{}"], { type: "application/json" }), "runtime-trace-session-a.json");

      expect(createObjectURL).toHaveBeenCalledOnce();
      expect(click).toHaveBeenCalledOnce();
      expect(document.querySelector('a[download="runtime-trace-session-a.json"]')).toBeNull();
      expect(revokeObjectURL).not.toHaveBeenCalled();
      vi.runAllTimers();
      expect(revokeObjectURL).toHaveBeenCalledWith("blob:runtime-trace");
    } finally {
      click.mockRestore();
      if (originalCreateObjectURL) {
        Object.defineProperty(URL, "createObjectURL", originalCreateObjectURL);
      } else {
        Reflect.deleteProperty(URL, "createObjectURL");
      }
      if (originalRevokeObjectURL) {
        Object.defineProperty(URL, "revokeObjectURL", originalRevokeObjectURL);
      } else {
        Reflect.deleteProperty(URL, "revokeObjectURL");
      }
      vi.useRealTimers();
    }
  });
});
