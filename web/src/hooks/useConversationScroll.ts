import { useCallback, useEffect, useRef, useState } from "react";
import type { RefObject } from "react";

// 会话消息区的"跟随最新"滚动逻辑：底部吸附、尺寸变化跟随、
// 用户上滚脱离跟随并显示"跳到最新"按钮。逻辑原样迁自 WebAgentPage。
export function useConversationScroll(deps: {
  selectedTaskId: number | null;
  messagesLength: number;
  latestMessageContentLength: number | undefined;
  eventsLength: number;
  running: boolean;
  showThinkingPlaceholder: boolean;
  streamState: string;
}): {
  conversationScrollRef: RefObject<HTMLDivElement | null>;
  setLatestMessageNode: (node: HTMLElement | null) => void;
  showJumpToLatest: boolean;
  scrollToLatest: (behavior?: ScrollBehavior) => void;
  handleConversationScroll: () => void;
  armFollowLatest: () => void;
} {
  const conversationScrollRef = useRef<HTMLDivElement | null>(null);
  const latestMessageRef = useRef<HTMLElement | null>(null);
  const latestMessageResizeObserverRef = useRef<ResizeObserver | null>(null);
  const followLatestAnimationFrameRef = useRef<number | null>(null);
  const lastConversationScrollTopRef = useRef(0);
  const followLatestRef = useRef(true);
  const [showJumpToLatest, setShowJumpToLatest] = useState(false);

  const cancelFollowLatestFrame = useCallback(() => {
    if (followLatestAnimationFrameRef.current !== null) {
      cancelAnimationFrame(followLatestAnimationFrameRef.current);
      followLatestAnimationFrameRef.current = null;
    }
  }, []);

  const scheduleFollowLatest = useCallback(() => {
    if (!followLatestRef.current || followLatestAnimationFrameRef.current !== null) {
      return;
    }
    followLatestAnimationFrameRef.current = requestAnimationFrame(() => {
      followLatestAnimationFrameRef.current = null;
      const container = conversationScrollRef.current;
      if (container && followLatestRef.current) {
        container.scrollTop = container.scrollHeight;
        lastConversationScrollTopRef.current = container.scrollTop;
      }
    });
  }, []);

  const setLatestMessageNode = useCallback((node: HTMLElement | null) => {
    if (latestMessageRef.current === node) {
      return;
    }
    cancelFollowLatestFrame();
    if (latestMessageRef.current) {
      latestMessageResizeObserverRef.current?.unobserve(latestMessageRef.current);
    }
    latestMessageRef.current = node;
    if (node) {
      latestMessageResizeObserverRef.current?.observe(node);
    }
  }, [cancelFollowLatestFrame]);

  useEffect(() => {
    if (typeof ResizeObserver !== "function") {
      return;
    }
    const observer = new ResizeObserver(() => {
      const container = conversationScrollRef.current;
      if (container && followLatestRef.current) {
        container.scrollTop = container.scrollHeight;
        lastConversationScrollTopRef.current = container.scrollTop;
      }
      scheduleFollowLatest();
    });
    latestMessageResizeObserverRef.current = observer;
    if (latestMessageRef.current) {
      observer.observe(latestMessageRef.current);
    }
    return () => {
      cancelFollowLatestFrame();
      observer.disconnect();
      latestMessageResizeObserverRef.current = null;
    };
  }, [cancelFollowLatestFrame, scheduleFollowLatest]);

  function scrollToLatest(behavior: ScrollBehavior = "auto") {
    const container = conversationScrollRef.current;
    if (!container) {
      return;
    }
    container.scrollTo({ top: container.scrollHeight, behavior });
    lastConversationScrollTopRef.current = container.scrollTop;
    followLatestRef.current = true;
    setShowJumpToLatest(false);
  }

  // A frame queued while the view was still following can land after the user has
  // scrolled up. Re-check at fire time — scrollToLatest also re-arms following and
  // hides the jump button, so an unguarded frame drags the reader back down and
  // makes the deliberate scroll look ignored. scheduleFollowLatest above checks the
  // same flag inside its own frame for this reason.
  function scrollToLatestIfFollowing() {
    if (!followLatestRef.current) {
      return;
    }
    scrollToLatest("auto");
  }

  function handleConversationScroll() {
    const container = conversationScrollRef.current;
    if (!container) {
      return;
    }
    const previousScrollTop = lastConversationScrollTopRef.current;
    const currentScrollTop = container.scrollTop;
    lastConversationScrollTopRef.current = currentScrollTop;
    const distanceFromBottom = container.scrollHeight - container.scrollTop - container.clientHeight;
    const following = distanceFromBottom < 96;
    if (following) {
      followLatestRef.current = true;
      setShowJumpToLatest(false);
    } else if (currentScrollTop < previousScrollTop - 1 || !followLatestRef.current) {
      followLatestRef.current = false;
      cancelFollowLatestFrame();
      setShowJumpToLatest(true);
    }
  }

  const armFollowLatest = useCallback(() => {
    followLatestRef.current = true;
  }, []);

  useEffect(() => {
    followLatestRef.current = true;
    setShowJumpToLatest(false);
    requestAnimationFrame(() => scrollToLatestIfFollowing());
  }, [deps.selectedTaskId]);

  useEffect(() => {
    if (followLatestRef.current) {
      requestAnimationFrame(() => scrollToLatestIfFollowing());
    } else if (deps.messagesLength > 0) {
      setShowJumpToLatest(true);
    }
  }, [deps.messagesLength, deps.latestMessageContentLength, deps.eventsLength, deps.running, deps.showThinkingPlaceholder, deps.streamState]);

  return {
    conversationScrollRef,
    setLatestMessageNode,
    showJumpToLatest,
    scrollToLatest,
    handleConversationScroll,
    armFollowLatest,
  };
}
