import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useConversationScroll } from "./useConversationScroll";

// The hook schedules "follow the latest message" through requestAnimationFrame.
// These tests hold the frame queue by hand instead of letting it fire on its own,
// because whether a queued frame lands before or after the user scrolls is pure
// machine speed: on CI it lands later than on a dev laptop, and the component
// test that covers the same behaviour was failing there and nowhere else.
describe("useConversationScroll", () => {
  let host: HTMLDivElement;
  let root: Root;
  let frames: FrameRequestCallback[];
  let originalRequestAnimationFrame: typeof globalThis.requestAnimationFrame;
  let originalScrollTo: typeof Element.prototype.scrollTo;
  let scrollTo: ReturnType<typeof vi.fn>;

  function Harness() {
    const scroll = useConversationScroll({
      selectedTaskId: 1,
      messagesLength: 1,
      latestMessageContentLength: 12,
      eventsLength: 2,
      running: false,
      showThinkingPlaceholder: false,
      streamState: "idle"
    });
    handleScroll = scroll.handleConversationScroll;
    return (
      <div
        data-testid="scroll"
        ref={scroll.conversationScrollRef}
        onScroll={scroll.handleConversationScroll}
      >
        {scroll.showJumpToLatest ? <span data-testid="jump">jump</span> : null}
      </div>
    );
  }

  let handleScroll: () => void = () => {};

  beforeEach(() => {
    // Synchronous act() only flushes state updates in an act environment, and the
    // assertions below read the jump indicator straight out of the DOM.
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    frames = [];
    originalRequestAnimationFrame = globalThis.requestAnimationFrame;
    globalThis.requestAnimationFrame = ((callback: FrameRequestCallback) => {
      frames.push(callback);
      return frames.length;
    }) as typeof globalThis.requestAnimationFrame;
    originalScrollTo = Element.prototype.scrollTo;
    scrollTo = vi.fn();
    Element.prototype.scrollTo = scrollTo as unknown as typeof Element.prototype.scrollTo;
    host = document.createElement("div");
    document.body.replaceChildren(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    globalThis.requestAnimationFrame = originalRequestAnimationFrame;
    Element.prototype.scrollTo = originalScrollTo;
    delete (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT;
  });

  function mount(): HTMLDivElement {
    act(() => {
      root.render(<Harness />);
    });
    const container = host.querySelector('[data-testid="scroll"]') as HTMLDivElement;
    Object.defineProperties(container, {
      scrollHeight: { configurable: true, value: 1_000 },
      scrollTop: { configurable: true, writable: true, value: 600 },
      clientHeight: { configurable: true, value: 400 }
    });
    return container;
  }

  function flushFrames(): void {
    const pending = frames;
    frames = [];
    act(() => {
      for (const frame of pending) {
        frame(0);
      }
    });
  }

  it("drops a frame queued before the user scrolled away", () => {
    const container = mount();
    expect(frames.length).toBeGreaterThan(0);

    // One scroll event at the bottom first: that is what records the baseline the
    // hook compares against to tell "the user scrolled up" from "content grew".
    act(() => handleScroll());
    container.scrollTop = 200;
    act(() => handleScroll());
    expect(host.querySelector('[data-testid="jump"]')).not.toBeNull();
    scrollTo.mockClear();

    flushFrames();

    expect(scrollTo).not.toHaveBeenCalled();
    expect(host.querySelector('[data-testid="jump"]')).not.toBeNull();
  });

  it("still follows when a queued frame lands while the view sits at the bottom", () => {
    mount();
    scrollTo.mockClear();

    flushFrames();

    expect(scrollTo).toHaveBeenCalledWith({ top: 1_000, behavior: "auto" });
    expect(host.querySelector('[data-testid="jump"]')).toBeNull();
  });
});
