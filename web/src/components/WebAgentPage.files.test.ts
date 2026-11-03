import { describe, expect, it } from "vitest";
import { summarizeActivity } from "./WebAgentPage";
import type { AgentTaskEventRecord } from "../lib/types";

// AUDIT-P2-10: the Files tab used to read payload.file / payload.path /
// payload.filename and sum insertions/deletions/line_delta — keys no Go writer
// has ever set. These tests pin it to the real file_change payload the server
// emits (internal/server/agent_task_file_events.go), so a rename on either side
// breaks loudly instead of silently emptying the tab.

let nextID = 1;

function fileChangeEvent(payload: Record<string, unknown>): AgentTaskEventRecord {
  return {
    id: nextID++,
    task_id: 1,
    event_type: "file_change",
    payload_json: JSON.stringify(payload),
    created_at: "2026-07-26T00:00:00Z",
    trace_id: ""
  };
}

function otherEvent(eventType: string, payload: Record<string, unknown>): AgentTaskEventRecord {
  return {
    id: nextID++,
    task_id: 1,
    event_type: eventType,
    payload_json: JSON.stringify(payload),
    created_at: "2026-07-26T00:00:00Z",
    trace_id: ""
  };
}

describe("summarizeActivity file tracking", () => {
  it("splits edited and read files from structured file_change events", () => {
    const events = [
      fileChangeEvent({ path: "/w/app.go", access: "edited", change: "modified", tool_name: "Edit", content_available: true, before_lines: 2, after_lines: 5, line_delta: 3 }),
      fileChangeEvent({ path: "/w/README.md", access: "read", tool_name: "Read" }),
      fileChangeEvent({ path: "/w/new.txt", access: "edited", change: "created", tool_name: "Write", content_available: true, before_lines: 0, after_lines: 4, line_delta: 4 })
    ];

    const activity = summarizeActivity(null, events);

    expect(activity.editedFiles.map((file) => file.path)).toEqual(["/w/app.go", "/w/new.txt"]);
    expect(activity.readFilePaths).toEqual(["/w/README.md"]);
    expect(activity.changedFiles).toBe(2);
    expect(activity.filesRead).toBe(1);
    expect(activity.lineDelta).toBe(7);
  });

  it("carries the change classification through for labelling", () => {
    const events = [
      fileChangeEvent({ path: "/w/gone.txt", access: "edited", change: "deleted", tool_name: "Bash", content_available: true, before_lines: 3, after_lines: 0, line_delta: -3 })
    ];

    const activity = summarizeActivity(null, events);

    expect(activity.editedFiles).toHaveLength(1);
    expect(activity.editedFiles[0].change).toBe("deleted");
    expect(activity.editedFiles[0].lineDelta).toBe(-3);
    expect(activity.lineDelta).toBe(-3);
  });

  it("does not invent a line delta when the content was externalized", () => {
    const events = [
      fileChangeEvent({ path: "/w/huge.bin", access: "edited", change: "modified", tool_name: "Bash", content_available: false })
    ];

    const activity = summarizeActivity(null, events);

    expect(activity.editedFiles).toHaveLength(1);
    expect(activity.editedFiles[0].lineDelta).toBeNull();
    expect(activity.editedFiles[0].contentAvailable).toBe(false);
    expect(activity.lineDelta).toBe(0);
  });

  it("counts one entry per path even when a file is touched repeatedly", () => {
    const events = [
      fileChangeEvent({ path: "/w/app.go", access: "edited", change: "created", tool_name: "Write", content_available: true, before_lines: 0, after_lines: 2, line_delta: 2 }),
      fileChangeEvent({ path: "/w/app.go", access: "edited", change: "modified", tool_name: "Edit", content_available: true, before_lines: 2, after_lines: 6, line_delta: 4 })
    ];

    const activity = summarizeActivity(null, events);

    expect(activity.editedFiles).toHaveLength(1);
    expect(activity.changedFiles).toBe(1);
    // The aggregate delta still sums every edit, and the surviving row keeps the
    // earliest classification: the file was created by this run.
    expect(activity.lineDelta).toBe(6);
    expect(activity.editedFiles[0].change).toBe("created");
    expect(activity.editedFiles[0].lineDelta).toBe(6);
  });

  it("promotes a file from read to edited once it is written", () => {
    const events = [
      fileChangeEvent({ path: "/w/app.go", access: "read", tool_name: "Read" }),
      fileChangeEvent({ path: "/w/app.go", access: "edited", change: "modified", tool_name: "Edit", content_available: true, before_lines: 1, after_lines: 2, line_delta: 1 })
    ];

    const activity = summarizeActivity(null, events);

    expect(activity.editedFiles.map((file) => file.path)).toEqual(["/w/app.go"]);
    expect(activity.readFilePaths).toEqual([]);
    expect(activity.changedFiles).toBe(1);
  });

  it("tracks a non-regular object without a line count", () => {
    const events = [
      fileChangeEvent({ path: "/w/sub", access: "edited", change: "created", object: "dir", tool_name: "Bash" })
    ];

    const activity = summarizeActivity(null, events);

    expect(activity.editedFiles[0].object).toBe("dir");
    expect(activity.editedFiles[0].lineDelta).toBeNull();
  });

  it("ignores the legacy guessed payload keys entirely", () => {
    // These are the keys the old implementation looked for. No Go writer emits
    // them, so honouring them would resurrect the fabricated data.
    const events = [
      otherEvent("tool_result", { tool_name: "Edit", file: "/w/ghost.go", insertions: 12, deletions: 4 }),
      otherEvent("tool_result", { tool_name: "Read", path: "/w/phantom.go" }),
      otherEvent("tool_result", { tool_name: "Write", filename: "/w/spectre.go", line_delta: 99 })
    ];

    const activity = summarizeActivity(null, events);

    expect(activity.editedFiles).toEqual([]);
    expect(activity.readFilePaths).toEqual([]);
    expect(activity.changedFiles).toBe(0);
    expect(activity.filesRead).toBe(0);
    expect(activity.lineDelta).toBe(0);
  });

  it("still reports command activity from tool events", () => {
    const events = [
      otherEvent("tool_result", { tool_name: "Bash", preview: "ok" }),
      fileChangeEvent({ path: "/w/out.txt", access: "edited", change: "created", tool_name: "Bash", content_available: true, before_lines: 0, after_lines: 1, line_delta: 1 })
    ];

    const activity = summarizeActivity(null, events);

    expect(activity.commandsRun).toBe(1);
    expect(activity.changedFiles).toBe(1);
    expect(activity.hasOperationalActivity).toBe(true);
  });

  it("surfaces the most recent file as the current file", () => {
    const events = [
      fileChangeEvent({ path: "/w/first.go", access: "read", tool_name: "Read" }),
      fileChangeEvent({ path: "/w/second.go", access: "edited", change: "modified", tool_name: "Edit", content_available: true, before_lines: 1, after_lines: 1, line_delta: 0 })
    ];

    expect(summarizeActivity(null, events).currentFile).toBe("/w/second.go");
  });
});
