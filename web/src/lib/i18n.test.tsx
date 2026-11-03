import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import ts from "typescript";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { I18nProvider, useI18n, webUIV2TranslationKeys, webUIV2Translations } from "./i18n";

function Labels() {
  const { t } = useI18n();
  return <>{t("webui2.brand")}|{t("webui2.newSession")}|{t("webui2.settingsTitle")}</>;
}

describe("WebUI v2 translations", () => {
  let host: HTMLDivElement;
  let root: Root;
  let storage: Map<string, string>;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    storage = new Map();
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        getItem: (key: string) => storage.get(key) ?? null,
        setItem: (key: string, value: string) => storage.set(key, value),
        removeItem: (key: string) => storage.delete(key),
        clear: () => storage.clear()
      }
    });
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  it("keeps the English and Chinese v2 catalogs complete and identical", () => {
    const english = webUIV2TranslationKeys("en");
    const chinese = webUIV2TranslationKeys("zh");

    expect(english).toEqual(chinese);
    expect(english.length).toBeGreaterThan(30);
    for (const value of Object.values(webUIV2Translations("en")).concat(Object.values(webUIV2Translations("zh")))) {
      expect(value.trim()).not.toBe("");
    }
  });

  it("localizes profile and language option labels without English leakage in Chinese", () => {
    const chinese = webUIV2Translations("zh");

    expect(chinese["webui2.profile"]).toBe("智能体配置");
    expect(chinese["webui2.noProfile"]).toBe("未分配智能体配置");
    expect(chinese["webui2.languageEnglish"]).toBe("英文");
    expect(chinese["webui2.languageChinese"]).toBe("中文");
  });

  it("keeps visible v2 JSX text behind translation keys", () => {
    const sourceFiles = [
      "src/v2/WebUIV2App.tsx",
      "src/v2/components/SessionSidebar.tsx",
      "src/v2/components/SettingsDrawer.tsx",
      "src/v2/components/ConversationWorkspace.tsx",
      "src/v2/components/ConversationMessage.tsx",
      "src/v2/components/OperationCard.tsx",
      "src/v2/components/EmptyState.tsx"
    ];
    const visibleLiterals = sourceFiles.flatMap((file) => {
      const source = readFileSync(resolve(process.cwd(), file), "utf8");
      const sourceFile = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
      const literals: string[] = [];
      function visit(node: ts.Node): void {
        if (ts.isJsxText(node) && /[A-Za-z\u4e00-\u9fff]/.test(node.text) && !/niuma/i.test(node.text)) {
          literals.push(`${file}:${node.text.trim()}`);
        }
        if (ts.isJsxAttribute(node) && ["aria-label", "placeholder", "title"].includes(node.name.getText(sourceFile)) && node.initializer && ts.isStringLiteral(node.initializer) && /[A-Za-z\u4e00-\u9fff]/.test(node.initializer.text)) {
          literals.push(`${file}:${node.initializer.text}`);
        }
        ts.forEachChild(node, visit);
      }
      visit(sourceFile);
      return literals;
    });

    expect(visibleLiterals).toEqual([]);
  });

  it("uses the saved Chinese preference for v2 controls", () => {
    storage.set("niuma-webui.language.v1", "zh");
    act(() => root.render(<I18nProvider><Labels /></I18nProvider>));

    expect(host.textContent).toContain("牛马 Niuma");
    expect(host.textContent).toContain("新建会话");
    expect(host.textContent).toContain("设置");
  });

  it("falls back to English for missing and invalid preferences", () => {
    act(() => root.render(<I18nProvider><Labels /></I18nProvider>));
    expect(host.textContent).toContain("New session");

    storage.set("niuma-webui.language.v1", "fr");
    act(() => root.unmount());
    root = createRoot(host);
    act(() => root.render(<I18nProvider><Labels /></I18nProvider>));
    expect(host.textContent).toContain("New session");
  });
});
