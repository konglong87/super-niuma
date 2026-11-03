import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../lib/i18n";
import { ThinkingMessage } from "./ThinkingMessage";

describe("thinking disclosure", () => {
  let host: HTMLDivElement;
  let root: Root;
  let contentHeight: number;
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
    contentHeight = 300;
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(() => contentHeight);
    vi.spyOn(window, "getComputedStyle").mockReturnValue({ lineHeight: "22.75px" } as CSSStyleDeclaration);
  });
  afterEach(() => {
    act(() => root.unmount());
    vi.restoreAllMocks();
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });
  const render = (content: string, full = false, live = true) => act(() => root.render(<I18nProvider><ThinkingMessage content={content} meta={<span>{live ? "Running" : "Completed"}</span>} full={full} live={live} /></I18nProvider>));
  const toggle = (open: boolean) => act(() => {
    const details = host.querySelector("details")!;
    details.open = open;
    details.dispatchEvent(new Event("toggle"));
  });

  it("renders semantic Markdown and code indentation after disclosure", () => {
    render("## Plan\n\nThe user asked for **formatting**.\n\n- First\n- Second\n\n```go\n\treturn true\n```");
    expect(host.querySelector(".agent-markdown")).toBeNull();
    toggle(true);
    expect(host.querySelector("h2")?.textContent).toBe("Plan");
    expect(host.querySelector("strong")?.textContent).toBe("formatting");
    expect(host.querySelectorAll("li")).toHaveLength(2);
    expect(host.querySelector("pre code")?.textContent).toContain("\treturn true");
  });

  it("keeps expanded content visible through deltas, completion and reopening", () => {
    render("Initial reasoning");
    toggle(true);
    expect(host.querySelector("[data-clamped]")?.getAttribute("data-clamped")).toBe("true");
    act(() => host.querySelector<HTMLButtonElement>(".webui2-thinking-expand")!.click());
    render("Initial reasoning with another paragraph", false, false);
    expect(host.querySelector("details")?.open).toBe(true);
    expect(host.querySelector("[data-clamped]")?.getAttribute("data-clamped")).toBe("false");
    expect(host.querySelector(".webui2-thinking-expand")?.getAttribute("aria-expanded")).toBe("true");
    toggle(false);
    toggle(true);
    expect(host.querySelector("[data-clamped]")?.getAttribute("data-clamped")).toBe("false");
    act(() => host.querySelector<HTMLButtonElement>(".webui2-thinking-expand")!.click());
    expect(host.querySelector("[data-clamped]")?.getAttribute("data-clamped")).toBe("true");
  });

  it("only offers expansion for overflowing previews and never clamps full mode", () => {
    contentHeight = 100;
    render("Short");
    toggle(true);
    expect(host.querySelector(".webui2-thinking-expand")).toBeNull();
    contentHeight = 500;
    render("Longer content");
    expect(host.querySelector(".webui2-thinking-expand")).not.toBeNull();
    render("Longer content", true);
    expect(host.querySelector("details")).toBeNull();
    expect(host.querySelector(".webui2-thinking-expand")).toBeNull();
    expect(host.querySelector("[data-clamped]")?.getAttribute("data-clamped")).toBe("false");
  });
});
