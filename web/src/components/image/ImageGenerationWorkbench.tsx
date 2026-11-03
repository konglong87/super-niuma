import { ArrowLeft, Image as ImageIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { listTenantSessions } from "../../lib/api";
import type { IdentityConfig, TenantSession } from "../../lib/types";
import { useI18n } from "../../lib/i18n";
import { ImageGenerationPanel } from "./ImageGenerationPanel";
import { agentCopy } from "../agent/copy";

type Props = {
  identity: IdentityConfig;
  initialSessionId?: number | null;
  initialPrompt?: string;
  onSessionChange?: (sessionId: number | undefined) => void;
};

export function imageWorkbenchHref(identity: IdentityConfig, sessionId?: number | null, prompt?: string): string {
  const url = new URL("/webui/", typeof window === "undefined" ? "http://localhost" : window.location.origin);
  url.searchParams.set("view", "images");
  if (sessionId) url.searchParams.set("session_id", String(sessionId));
  if (prompt?.trim()) url.searchParams.set("prompt", prompt.trim());
  const token = new URLSearchParams(typeof window === "undefined" ? "" : window.location.search).get("token") || identity.apiToken.trim();
  if (token) url.searchParams.set("token", token);
  return `${url.pathname}${url.search}`;
}

export function ImageGenerationWorkbench({ identity, initialSessionId, initialPrompt = "", onSessionChange }: Props) {
  const { language } = useI18n();
  const [sessions, setSessions] = useState<TenantSession[]>([]);
  const [sessionID, setSessionID] = useState<number | undefined>(initialSessionId || undefined);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const onSessionChangeRef = useRef(onSessionChange);

  useEffect(() => {
    onSessionChangeRef.current = onSessionChange;
  }, [onSessionChange]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    listTenantSessions(identity)
      .then((items) => {
        if (cancelled) return;
        setSessions(items);
        const preferred = initialSessionId && items.some((item) => item.id === initialSessionId)
          ? initialSessionId
          : items[0]?.id;
        setSessionID(preferred);
        onSessionChangeRef.current?.(preferred);
      })
      .catch((cause) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : String(cause));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => { cancelled = true; };
  }, [identity.apiBase, identity.apiToken, identity.mobileJwt, identity.tenantKey, identity.userId, initialSessionId]);

  const selected = useMemo(() => sessions.find((item) => item.id === sessionID), [sessionID, sessions]);

  return (
    <section className="image-generation-workbench" aria-label={language === "zh" ? "图片生成页面" : "Image generation page"}>
      <header className="image-workbench-header">
        <div className="image-workbench-title">
          <ImageIcon size={22} aria-hidden="true" />
          <div>
            <h1>{language === "zh" ? "图片生成" : "Image generation"}</h1>
            <p>{language === "zh" ? "选择已配置的图片模型，生成、编辑和管理当前租户的图片资产。" : "Choose a configured image model to generate, edit, and manage tenant-scoped image assets."}</p>
          </div>
        </div>
        <a className="secondary-button compact-action" href={identity.apiBase ? "/webui/" : "/"}>
          <ArrowLeft size={15} aria-hidden="true" />
          {language === "zh" ? "返回主界面" : "Back to workspace"}
        </a>
      </header>

      <div className="image-workbench-toolbar">
        <label>
          <span>{language === "zh" ? "会话" : "Image session"}</span>
          <select aria-label={language === "zh" ? "图片会话" : "Image session"} value={sessionID || ""} onChange={(event) => {
            const next = Number(event.target.value) || undefined;
            setSessionID(next);
            onSessionChange?.(next);
          }} disabled={loading || sessions.length === 0}>
            {sessions.length === 0 ? <option value="">{loading ? (language === "zh" ? "加载中" : "Loading") : (language === "zh" ? "暂无会话" : "No sessions")}</option> : null}
            {sessions.map((session) => <option key={session.id} value={session.id}>{session.title || `Session ${session.id}`} · #{session.id}</option>)}
          </select>
        </label>
        {selected ? <span className="image-workbench-session-meta">{selected.cwd || selected.session_key}</span> : null}
      </div>

      {error ? <p className="agent-inline-error">{error}</p> : null}
      {sessionID ? <ImageGenerationPanel identity={identity} sessionId={sessionID} copy={agentCopy[language]} initialPrompt={initialPrompt} /> : <div className="image-workbench-empty">{language === "zh" ? "请先选择一个会话。" : "Select a session to start."}</div>}
    </section>
  );
}
