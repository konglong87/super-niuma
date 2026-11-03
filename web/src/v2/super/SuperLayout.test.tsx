import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
const appCSS = readFileSync("src/styles/app.css", "utf8");
const workspaceCSS = readFileSync("src/v2/super/superWorkspace.css", "utf8");
const platformCSS = readFileSync("src/v2/super/platformPanel.css", "utf8");
import { SuperConsent } from "./SuperConsent";

// CSS/DOM regression contracts, not native screenshot acceptance. Load the
// actual inherited form rules that caused the screenshot's 100%-wide checkbox.
const inheritedInput = [...appCSS.matchAll(/(?:^|\n)input,\s*textarea,\s*select\s*\{[^}]*\}/g)].map(([rule]) => rule).join("\n");
const inheritedLabel = appCSS.match(/(?:^|\n)label\s*\{[^}]*\}/)?.[0] ?? "";
describe("Super isolated layout contracts", () => {
  let root: Root;
  let host: HTMLDivElement;
  let style: HTMLStyleElement;
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    style = document.createElement("style"); style.textContent = `${inheritedInput}\n${inheritedLabel}\n${workspaceCSS}\n${platformCSS}`; document.head.append(style);
    host = document.createElement("div"); host.className = "webui2-page"; document.body.replaceChildren(host); root = createRoot(host);
  });
  afterEach(() => { act(() => root.unmount()); style.remove(); host.remove(); delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT; });
  it("overrides actual legacy width/min-height/padding only inside Super", () => {
    expect(inheritedInput).toContain("width: 100%"); expect(inheritedInput).toContain("min-height: 38px");
    act(() => root.render(<><input type="checkbox" id="outside-super" /><section className="super-workspace"><div className="super-platform-submit-footer"><SuperConsent checked={false} disabled={false} label="I confirm the workspace, budgets and delivery targets before authorizing this exact grant." onChange={() => {}} /></div></section></>));
    const checkbox = host.querySelector<HTMLInputElement>(".super-consent input")!;
    const css = getComputedStyle(checkbox);
    expect(css.width).toBe("18px"); expect(css.minWidth).toBe("18px"); expect(css.maxWidth).toBe("18px");
    expect(css.height).toBe("18px"); expect(css.minHeight).toBe("18px"); expect(css.padding).toBe("0px");
    expect(getComputedStyle(host.querySelector("#outside-super")!).width).toBe("100%");
    expect(checkbox.parentElement?.lastElementChild?.tagName).toBe("SPAN");
    const label = getComputedStyle(checkbox.parentElement!);
    expect(label.display).toBe("grid"); expect(label.gridTemplateColumns).toBe("18px minmax(0, 1fr)");
    expect(parseFloat(label.minWidth)).toBe(0); expect(label.whiteSpace).toBe("normal");
    expect(getComputedStyle(checkbox.parentElement!.lastElementChild!).overflowWrap).toBe("anywhere");
  });
  it("keeps a keyboard-focusable native select with a scoped 40px appearance reset", () => {
    let changed = false;
    act(() => root.render(<><select id="outside-select"><option>Legacy</option></select><section className="super-workspace"><div className="super-platform"><label htmlFor="styled-select">Profile</label><select id="styled-select" onChange={() => { changed = true; }}><option value="one">One</option><option value="two">Two</option></select></div></section></>));
    const select = host.querySelector<HTMLSelectElement>("#styled-select")!;
    const css = getComputedStyle(select);
    expect(css.appearance).toBe("none"); expect(css.height).toBe("40px"); expect(css.minHeight).toBe("40px"); expect(css.paddingRight).toBe("38px");
    expect(css.backgroundImage).toContain("linear-gradient");
    expect(platformCSS).toContain("-webkit-appearance: none");
    expect(getComputedStyle(host.querySelector("#outside-select")!).appearance).not.toBe("none");
    expect(host.querySelector<HTMLLabelElement>('label[for="styled-select"]')?.control).toBe(select);
    expect(select.tagName).toBe("SELECT"); expect(select.tabIndex).toBe(0);
    act(() => { select.focus(); select.value = "two"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(document.activeElement).toBe(select); expect(changed).toBe(true); expect(select.value).toBe("two");
  });
  it("bounds the page and allows every primary grid/card to shrink without clipping content", () => {
    act(() => root.render(<section className="super-workspace"><div className="super-columns"><div className="super-card"><div className="super-platform-selectors"><label>Profile<select><option>{"Long profile ".repeat(20)}</option></select></label></div></div></div></section>));
    const page = getComputedStyle(host.querySelector(".super-workspace")!);
    expect(page.width).toBe("100%"); expect(page.maxWidth).toBe("1200px"); expect(parseFloat(page.minWidth)).toBe(0); expect(page.boxSizing).toBe("border-box");
    expect(parseFloat(getComputedStyle(host.querySelector(".super-card")!).minWidth)).toBe(0);
    expect(getComputedStyle(host.querySelector(".super-columns")!).gridTemplateColumns).toContain("minmax(min(100%, 400px), 1fr)");
    expect(getComputedStyle(host.querySelector(".super-platform-selectors")!).gridTemplateColumns).toContain("minmax(min(100%, 250px), 1fr)");
    expect(workspaceCSS).not.toContain(".super-form input, .super-form textarea");
    expect(platformCSS).not.toContain("overflow-x: hidden");
  });
});
