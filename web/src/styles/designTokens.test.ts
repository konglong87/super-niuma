import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const css = readFileSync(resolve(process.cwd(), "src/styles/app.css"), "utf8");
const webUIV2CSS = readFileSync(resolve(process.cwd(), "src/v2/styles.css"), "utf8");

// 设计契约守护:web-agent 样式段禁止裸 hex 色值,颜色只能来自
// .web-agent-page 上的 --agent-* token 定义块。
// 规则见 docs/web_agent/web_agent_design_system.md。
describe("web agent design tokens", () => {
  it("keeps the web-agent style section free of raw hex colors", () => {
    const start = css.indexOf(".web-agent-page {");
    expect(start).toBeGreaterThan(-1);
    const section = css.slice(start);
    const tokenBlockEnd = section.indexOf("display: grid;");
    expect(tokenBlockEnd).toBeGreaterThan(-1);
    const body = section.slice(tokenBlockEnd);
    const hexes = body.match(/#[0-9a-fA-F]{3,8}\b/g) ?? [];
    expect(hexes).toEqual([]);
  });

  it("defines compact conversation density tokens and applies them to markdown", () => {
    expect(css).toContain("--agent-content-line-height: 1.52;");
    expect(css).toContain("--agent-markdown-block-gap: 6px;");
    expect(css).toContain("--agent-list-gap: 5px;");
    expect(css).toContain("--agent-message-gap: 9px;");
    expect(css).toContain("--agent-message-content-width: 100%;");
    expect(css).toContain("line-height: var(--agent-content-line-height);");
    expect(css).toContain("gap: var(--agent-markdown-block-gap);");
    expect(css).toContain("gap: var(--agent-list-gap);");
    expect(css).toContain("margin: 0 auto var(--agent-message-gap);");
    expect(css).toContain("width: var(--agent-message-content-width);");
  });

  it("keeps the composer compact and preserves semantic next-step labeling", () => {
    expect(css).toContain("--agent-composer-textarea-default-height: 36px;");
    expect(css).toContain(".agent-composer-shell {");
    expect(css).toContain("flex-direction: column;");
    expect(css).toContain(".agent-composer-input {");
    expect(css).toContain(".next-step-hint {");
    expect(css).toContain("clip: rect(0, 0, 0, 0);");
  });
});

// 设计契约守护:全局配置编辑器(SettingsPanel)样式段同样禁止裸 hex,
// 颜色只能引用 :root 上的全局语义 token。该段目前位于 web-agent 段之后,
// 上面的测试也会扫到它;单独守护是为了防止样式段挪动后契约静默失效。
describe("global settings editor design tokens", () => {
  const marker = "/* ------- Global settings editor ------- */";

  it("keeps the settings editor style section free of raw hex colors", () => {
    const start = css.indexOf(marker);
    expect(start).toBeGreaterThan(-1);
    const section = css.slice(start);
    const hexes = section.match(/#[0-9a-fA-F]{3,8}\b/g) ?? [];
    expect(hexes).toEqual([]);
  });

  it("only references tokens that are defined in app.css", () => {
    // CSS 对未定义的 var() 引用静默失效,这里把拼错 token 名变成显式失败。
    const start = css.indexOf(marker);
    expect(start).toBeGreaterThan(-1);
    const section = css.slice(start);
    const refs = new Set(
      [...section.matchAll(/var\((--[a-z0-9-]+)\)/g)].map((match) => match[1]),
    );
    expect(refs.size).toBeGreaterThan(0);
    for (const ref of refs) {
      expect(css, `token ${ref} is referenced but never defined`).toContain(`${ref}:`);
    }
  });
});

describe("WebUI v2 design tokens", () => {
  const rootSelector = ".webui2-page {";
  const rootStart = webUIV2CSS.indexOf(rootSelector);
  const rootEnd = webUIV2CSS.indexOf("}", rootStart);
  const rootTokenBlock = webUIV2CSS.slice(rootStart, rootEnd + 1);
  const darkTokenBlockPattern = /\.webui2-page\[data-theme="dark"\],[\s\S]*?\n}\n/;
  const darkTokenBlock = webUIV2CSS.match(darkTokenBlockPattern)?.[0] ?? "";
  const styleBody = webUIV2CSS.replace(rootTokenBlock, "").replace(darkTokenBlock, "");

  it("keeps WebUI v2 token references defined by its scoped token blocks", () => {
    expect(rootStart).toBeGreaterThan(-1);
    expect(rootEnd).toBeGreaterThan(rootStart);
    const definitions = new Set(
      [...`${rootTokenBlock}\n${darkTokenBlock}`.matchAll(/(--webui2-[a-z0-9-]+)\s*:/g)].map((match) => match[1]),
    );
    const references = new Set(
      [...styleBody.matchAll(/var\((--webui2-[a-z0-9-]+)\)/g)].map((match) => match[1]),
    );
    expect(references.size).toBeGreaterThan(0);
    for (const token of references) {
      expect(definitions, `token ${token} is referenced outside its definition block`).toContain(token);
    }
  });

  it("avoids gradients and raw hex outside WebUI v2 token definitions", () => {
    expect(styleBody).not.toMatch(/linear-gradient|radial-gradient/);
    expect(styleBody.match(/#[0-9a-fA-F]{3,8}\b/g) ?? []).toEqual([]);
  });

  it("keeps the desktop dock, mobile overlay, and reduced-motion layout guards", () => {
    const mobileBreakpoint = "@media (max-width: 760px)";
    const mobileStart = webUIV2CSS.indexOf(mobileBreakpoint);
    expect(mobileStart).toBeGreaterThan(-1);
    const desktopStyles = webUIV2CSS.slice(0, mobileStart);
    const mobileAndMotionStyles = webUIV2CSS.slice(mobileStart);
    expect(desktopStyles).toContain('.webui2-page[data-inspector-open="true"] .webui2-content { grid-template-columns: minmax(0, 1fr) minmax(260px, 340px); }');
    expect(mobileAndMotionStyles).toContain('.webui2-page[data-inspector-open="true"] .webui2-content { grid-template-columns: minmax(0, 1fr); }');
    expect(mobileAndMotionStyles).toContain(".webui2-page .webui2-inspector { bottom: 0;");
    expect(mobileAndMotionStyles).toContain("@media (prefers-reduced-motion: reduce)");
  });
});
