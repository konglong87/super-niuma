import {
  createMobileSession,
  saveDocument,
  saveMemory,
  saveProfile,
  saveSkillOverride,
  saveTenantSkill,
  saveTenantUser
} from "./api";
import type { IdentityConfig } from "./types";

export type ScenarioSeedResult = {
  sessionId: number;
  prompt: string;
};

export const scenarioPrompt = [
  "Run the WebUI validation scenario.",
  "Use any relevant skill if available.",
  "Remember that this user prefers concise Go-focused answers.",
  "Mention whether long-term memory, short-term document context, profile, skills, trace, telemetry, token usage, and prompt cache can be inspected."
].join("\n");

export async function seedValidationScenario(identity: IdentityConfig): Promise<ScenarioSeedResult> {
  const suffix = Date.now().toString(36);
  const version = Math.floor(Date.now() / 1000);
  await seedDemoUsers(identity);
  await saveMemory(identity, {
    memory_key: "webui.validation.preference",
    category: "webui",
    content: "User prefers concise Go-focused answers and wants full mobile chat lifecycle validation.",
    importance: 8,
    source: "webui-scenario"
  });
  await saveProfile(
    identity,
    JSON.stringify({ source: "webui-scenario", traits: ["go", "mobile-chat", "validation"], updated_at: new Date().toISOString() }),
    "Go developer validating mobile chat full-cycle WebUI flows.",
    version
  );
  await saveDocument(identity, {
    doc_type: "CLAUDE.md",
    title: "WebUI Scenario Instructions",
    content_md: "# WebUI Scenario\n\nValidate memory, profile, skills, telemetry, trace, token usage, and prompt cache.",
    version,
    active: true
  });
  await saveTenantSkill(identity, {
    skill_key: "webui-validation",
    name: "WebUI Validation",
    version: 1,
    enabled: true,
    content_md: "---\ndescription: Validate WebUI mobile chat lifecycle\n---\nCheck session, messages, memory, profile, skills, telemetry, trace, token usage, and prompt cache evidence."
  });
  await saveSkillOverride(identity, {
    skill_key: "webui-validation",
    version: 1,
    enabled: true,
    config_json: JSON.stringify({ source: "webui-scenario" })
  });
  const sessionId = await createMobileSession(identity, `WebUI Scenario ${suffix}`);
  return { sessionId, prompt: scenarioPrompt };
}

async function seedDemoUsers(identity: IdentityConfig): Promise<void> {
  const demoUsers = [
    {
      user_key: identity.userId,
      email: `${identity.userId}@example.test`,
      display_name: displayName(identity.userId),
      role: "owner",
      status: "active",
      user_info_json: JSON.stringify({ source: "webui-demo-seed", current: true })
    },
    {
      user_key: `${identity.tenantKey}-owner`,
      email: `${identity.tenantKey}-owner@example.test`,
      display_name: `${displayName(identity.tenantKey)} Owner`,
      role: "owner",
      status: "active",
      user_info_json: JSON.stringify({ source: "webui-demo-seed", persona: "owner" })
    },
    {
      user_key: `${identity.tenantKey}-member`,
      email: `${identity.tenantKey}-member@example.test`,
      display_name: `${displayName(identity.tenantKey)} Member`,
      role: "member",
      status: "active",
      user_info_json: JSON.stringify({ source: "webui-demo-seed", persona: "member" })
    }
  ];

  await Promise.all(demoUsers.map((user) => saveTenantUser(identity, user)));
}

function displayName(value: string): string {
  return value
    .split(/[-_\s]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}
