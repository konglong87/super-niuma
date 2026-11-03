import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { useConversationScroll } from "./useConversationScroll";

type HookResult = ReturnType<typeof useConversationScroll>;

const deps = {
  selectedTaskId: 1,
  messagesLength: 2,
  latestMessageContentLength: 10,
  eventsLength: 0,
  running: false,
  showThinkingPlaceholder: false,
  streamState: "idle",
};

let host: HTMLDivElement;
let root: Root;
let current: HookResult;

function Harness() {
  current = useConversationScroll(deps);
  return null;
}

function makeContainer() {
  const container = {
    scrollTop: 0,
    scrollHeight: 1000,
    clientHeight: 100,
    scrollTo({ top }: { top: number }) {
      container.scrollTop = top;
    },
  };
  return container as unknown as HTMLDivElement;
}

beforeEach(() => {
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => {
    root.render(createElement(Harness));
  });
});

afterEach(() => {
  act(() => {
    root.unmount();
  });
  host.remove();
});

describe("useConversationScroll", () => {
  it("scrollToLatest 吸附到底部并隐藏跳转按钮", () => {
    const container = makeContainer();
    current.conversationScrollRef.current = container;
    act(() => {
      current.scrollToLatest();
    });
    expect(container.scrollTop).toBe(container.scrollHeight);
    expect(current.showJumpToLatest).toBe(false);
  });

  it("用户上滚脱离跟随后显示跳转按钮，回到底部后隐藏", () => {
    const container = makeContainer();
    current.conversationScrollRef.current = container;
    container.scrollTop = 500;
    act(() => {
      current.handleConversationScroll();
    });
    container.scrollTop = 400;
    act(() => {
      current.handleConversationScroll();
    });
    expect(current.showJumpToLatest).toBe(true);
    container.scrollTop = container.scrollHeight - container.clientHeight;
    act(() => {
      current.handleConversationScroll();
    });
    expect(current.showJumpToLatest).toBe(false);
  });
});
