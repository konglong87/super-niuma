import { describe, expect, it } from "vitest";
import { blankNode, nextNodeID, removeNode, validateTemplate, withPreset } from "./editor";
import { templateFixture } from "./testFixtures";
describe("static Workflow DAG", () => {
  it("validates a real fixed agent binding and rejects unbound steps", () => {
    const template = templateFixture(); expect(validateTemplate(template)).toEqual([]);
    template.agents = []; expect(validateTemplate(template)).toContain("step");
  });
  it("creates sequential/parallel presets, removes dangling edges and preserves custom join", () => {
    const definition = templateFixture().definition;
    definition.nodes.push(blankNode("step-2"), blankNode("step-3"));
    const sequential = withPreset(definition, "sequential");
    expect(sequential.nodes.map((node) => node.depends_on)).toEqual([[], ["step-1"], ["step-2"]]);
    expect(withPreset(definition, "parallel").nodes.every((node) => !node.depends_on.length)).toBe(true);
    sequential.nodes[2].depends_on = ["step-1", "step-2"];
    expect(withPreset(sequential, "custom")).toBe(sequential);
    expect(removeNode(sequential, "step-2").nodes[1].depends_on).toEqual(["step-1"]);
    expect(nextNodeID(sequential)).toBe("step-4");
  });
  it("rejects cycles, missing/self edges and unsafe budgets", () => {
    const template = templateFixture();
    template.definition.nodes.push({ ...blankNode("step-2"), instruction: "Join", agent_ref: "writer", depends_on: ["step-1"] });
    template.definition.nodes[0].depends_on = ["step-2"];
    expect(validateTemplate(template)).toContain("cycle");
    template.definition.nodes[0].depends_on = ["missing"]; expect(validateTemplate(template)).toContain("dependency");
    template.definition.nodes[0].depends_on = ["step-1"]; expect(validateTemplate(template)).toContain("dependency");
    template.definition.limits.max_parallel = 5; expect(validateTemplate(template)).toContain("limits");
    template.definition.nodes[0].retry.backoff_ms = 10001; expect(validateTemplate(template)).toContain("limits");
  });
});
