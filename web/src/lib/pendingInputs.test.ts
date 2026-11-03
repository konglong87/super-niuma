import { describe, expect, it } from "vitest";
import { movePendingInputUp, pendingInputDisplayContent, replacePendingInput, sortPendingInputs } from "./pendingInputs";
import type { PendingInputRecord } from "./types";

const item = (id: string, sequence: number, content = id): PendingInputRecord => ({ id, client_input_id: id, session_id: "5", content, sequence, status: "queued" });

describe("pending input helpers", () => {
  it("sorts by current queue sequence", () => {
    expect(sortPendingInputs([item("b", 2), item("a", 1)]).map((value) => value.id)).toEqual(["a", "b"]);
  });

  it("replaces an event update without duplicating the candidate", () => {
    expect(replacePendingInput([item("a", 1)], { ...item("a", 1), content: "updated" })).toEqual([{ ...item("a", 1), content: "updated" }]);
  });

  it("renders direction and original message transparently", () => {
    expect(pendingInputDisplayContent({ ...item("a", 1, "inspect"), direction: "focus on errors" })).toBe("[方向]\nfocus on errors\n\n[原消息]\ninspect");
  });

  it("moves a candidate one slot upward", () => {
    expect(movePendingInputUp([item("a", 1), item("b", 2), item("c", 3)], "b").map((value) => value.id)).toEqual(["b", "a", "c"]);
  });
});
