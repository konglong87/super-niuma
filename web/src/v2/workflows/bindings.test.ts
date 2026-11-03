import { describe, expect, it } from "vitest";
import { resolveAgentBinding, withAgentBindingIDs, workflowAgentChoices } from "./bindings";
import { agentFixture } from "./testFixtures";
import type { Binding } from "./types";
describe("Workflow exact Profile binding identity", () => {
  const legacy: Binding = { ref: "reviewer", profile_key: "writer", profile_version: 1 };
  const shared = { ...agentFixture(), record: { ...agentFixture().record, id: 11, scope: "tenant_shared" } };
  const privateAgent = { ...agentFixture(), record: { ...agentFixture().record, id: 22, scope: "user_private" } };
  it("uses unique database IDs as selector values even when key and version are identical", () => {
    const choices = workflowAgentChoices([shared, privateAgent], [legacy]);
    expect(choices.map((choice) => choice.value)).toEqual(["11", "22"]);
    expect(choices[0].binding.ref).not.toBe(choices[1].binding.ref);
    expect(choices[1].binding).toEqual({ ...legacy, profile_id: 22 });
    expect(choices[0].binding.profile_id).toBe(11);
  });
  it("preserves explicit shared IDs rather than silently preferring private", () => {
    const binding = { ...legacy, profile_id: 11 };
    expect(resolveAgentBinding(binding, [privateAgent, shared])?.record.scope).toBe("tenant_shared");
    expect(withAgentBindingIDs([binding], [privateAgent, shared])).toEqual([binding]);
  });
  it("upgrades legacy identity by exact key/version, preferring private before shared", () => {
    expect(withAgentBindingIDs([legacy], [shared, privateAgent])).toEqual([{ ...legacy, profile_id: 22 }]);
    expect(resolveAgentBinding({ ...legacy, profile_version: 2 }, [privateAgent, shared])).toBeUndefined();
    expect(resolveAgentBinding({ ...legacy, profile_id: 99 }, [privateAgent, shared])).toBeUndefined();
  });
  it("does not resolve ID=0, mismatched versions or unpublished catalog entries", () => {
    const zero = { ...privateAgent, record: { ...privateAgent.record, id: 0 } };
    expect(workflowAgentChoices([zero], [legacy])).toEqual([]);
    expect(resolveAgentBinding({ ...legacy, profile_id: 0 }, [zero])).toBeUndefined();
  });
});
