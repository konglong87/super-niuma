import { useEffect, useState } from "react";

const CLOCK_INTERVAL_MS = 1000;

// One clock belongs to the selected conversation, regardless of message count.
export function useConversationClock(active: boolean): number {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    if (!active) return;
    let timer: ReturnType<typeof setInterval> | undefined;
    const update = () => {
      clearInterval(timer);
      timer = undefined;
      if (document.visibilityState === "hidden") return;
      setNow(Date.now());
      timer = setInterval(() => setNow(Date.now()), CLOCK_INTERVAL_MS);
    };
    update();
    document.addEventListener("visibilitychange", update);
    return () => { clearInterval(timer); document.removeEventListener("visibilitychange", update); };
  }, [active]);
  return now;
}
