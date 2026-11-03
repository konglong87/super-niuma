import { describe, expect, it } from "vitest";
import { SETTINGS_NAV_ITEMS } from "./settingsRegistry";

describe("settings navigation semantics", () => {
  it("uses distinct beginner-facing names for definition, assignment, account and lifecycle concerns", () => {
    const byKey = new Map(SETTINGS_NAV_ITEMS.map((item) => [item.key, item]));
    const conceptKeys = ["profiles", "agent", "feishu", "provisioning"] as const;
    expect(byKey.get("profiles")?.zh).toBe("智能体定义");
    expect(byKey.get("agent")?.zh).toBe("入口分配");
    expect(byKey.get("feishu")?.zh).toBe("飞书连接");
    expect(byKey.get("provisioning")?.zh).toBe("Worker 运行");
    expect(new Set(conceptKeys.map((key) => byKey.get(key)?.zh)).size).toBe(4);
  });
});
