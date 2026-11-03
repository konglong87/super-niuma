import { describe, expect, it } from "vitest";
import { appendStreamText } from "./streamText";

describe("stream text assembly", () => {
  it("inserts a boundary space between adjacent English words", () => {
    expect(appendStreamText("The user asks", "again about model")).toBe("The user asks again about model");
  });

  it("keeps CJK text and existing whitespace unchanged", () => {
    expect(appendStreamText("你好", "世界")).toBe("你好世界");
    expect(appendStreamText("hello ", "world")).toBe("hello world");
  });

  it("does not insert spaces inside fenced code", () => {
    expect(appendStreamText("```js\nconst foo", "Bar\n```")).toBe("```js\nconst fooBar\n```");
  });

  it("does not split inline code or camelCase identifiers", () => {
    expect(appendStreamText("Use `foo", "Bar` here")).toBe("Use `fooBar` here");
    expect(appendStreamText("call getUser", "Name() now")).toBe("call getUserName() now");
  });

  it("restores readable punctuation and CJK to Latin boundaries", () => {
    expect(appendStreamText("tenant.", "Let me check")).toBe("tenant. Let me check");
    expect(appendStreamText('5.2"', "and explain")).toBe('5.2" and explain');
    expect(appendStreamText("模型", "Model")).toBe("模型 Model");
    expect(appendStreamText("Model", "模型")).toBe("Model 模型");
    expect(appendStreamText("https:", "//example.com")).toBe("https://example.com");
    expect(appendStreamText("environment shows", "Model:")).toBe("environment shows Model:");
    expect(appendStreamText("connection to", "GLM5.2")).toBe("connection to GLM5.2");
    expect(appendStreamText("Model:", "glm-5.2")).toBe("Model: glm-5.2");
    expect(appendStreamText("GLM", "5.2")).toBe("GLM 5.2");
    expect(appendStreamText("sha", "256")).toBe("sha256");
  });
});
