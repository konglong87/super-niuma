import { CheckCircle2, Database, FileJson2, LoaderCircle, RotateCw } from "lucide-react";
import { useEffect, useState, type JSX } from "react";
import { useI18n } from "../../lib/i18n";
import type { DesktopServiceBridge } from "../desktopServiceBridge";

type SessionBackend = "jsonl" | "sqlite";

type Props = {
  bridge: DesktopServiceBridge | null;
  onDirtyChange: (dirty: boolean) => void;
  onBusyChange: (busy: boolean) => void;
};

export function SessionBackendSettingsPanel({ bridge, onDirtyChange, onBusyChange }: Props): JSX.Element {
  const { language } = useI18n();
  const zh = language === "zh";
  const [saved, setSaved] = useState<SessionBackend>("jsonl");
  const [draft, setDraft] = useState<SessionBackend>("jsonl");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  const available = Boolean(bridge?.GetSessionBackend && bridge.SetSessionBackend);

  useEffect(() => {
    let active = true;
    onBusyChange(true);
    if (!bridge?.GetSessionBackend) {
      setLoading(false);
      onBusyChange(false);
      return;
    }
    void bridge.GetSessionBackend().then((value) => {
      if (!active) return;
      const backend: SessionBackend = value === "sqlite" ? "sqlite" : "jsonl";
      setSaved(backend);
      setDraft(backend);
    }).catch((reason: unknown) => {
      if (!active) return;
      setError(reason instanceof Error ? reason.message : (zh ? "读取会话存储配置失败" : "Unable to read session storage settings"));
    }).finally(() => {
      if (!active) return;
      setLoading(false);
      onBusyChange(false);
    });
    return () => { active = false; };
  }, [bridge, onBusyChange, zh]);

  const dirty = draft !== saved;
  useEffect(() => { onDirtyChange(dirty); }, [dirty, onDirtyChange]);

  async function save(): Promise<void> {
    if (!bridge?.SetSessionBackend || !dirty) return;
    setSaving(true);
    onBusyChange(true);
    setStatus("");
    setError("");
    try {
      await bridge.SetSessionBackend(draft);
      setSaved(draft);
      setStatus(zh ? "已保存，本地服务已重启。" : "Saved. The local service has restarted.");
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : (zh ? "保存会话存储配置失败" : "Unable to save session storage settings"));
    } finally {
      setSaving(false);
      onBusyChange(false);
    }
  }

  if (!available) {
    return <section className="settings-section session-backend-panel"><div className="settings-notice">{zh ? "会话存储设置仅适用于 desktop-v2。" : "Session storage settings are available only in desktop-v2."}</div></section>;
  }

  return <div className="session-backend-panel">
    <section className="settings-section">
      <div className="session-backend-heading"><div><h2>{zh ? "聊天事件存储" : "Chat event storage"}</h2><p>{zh ? "桌面端会话列表和控制状态仍由 SQLite 管理；这里选择聊天事件正文的权威存储。" : "SQLite continues to manage the desktop session index and control state; this selects the authoritative store for chat event content."}</p></div><span className="session-backend-current"><Database size={16} />{saved.toUpperCase()}</span></div>
      <label className="session-backend-field" htmlFor="session-backend-select">
        {zh ? "会话存储模式" : "Session storage mode"}
        <select
          aria-label={zh ? "会话存储模式" : "Session storage mode"}
          className="session-backend-native-select"
          disabled={loading || saving}
          id="session-backend-select"
          onChange={(event) => { setDraft(event.target.value === "sqlite" ? "sqlite" : "jsonl"); setStatus(""); }}
          value={draft}
        >
          <option value="jsonl">{zh ? "JSONL（默认，低频 SQLite 写入）" : "JSONL (default, low SQLite write frequency)"}</option>
          <option value="sqlite">{zh ? "SQLite（回滚和兼容验证模式）" : "SQLite (rollback and compatibility mode)"}</option>
        </select>
      </label>
      <div className="settings-notice session-backend-notice"><RotateCw size={16} /><span>{zh ? "切换只对新启动的服务生效。保存时会自动重启本地服务；不会双写，也不会自动迁移已有聊天事件。" : "The choice applies at service startup. Saving restarts the local service; events are not dual-written or migrated automatically."}</span></div>
      {loading ? <p className="session-backend-status" role="status"><LoaderCircle className="session-backend-spinner" size={15} />{zh ? "正在读取…" : "Reading…"}</p> : null}
      {status ? <p className="session-backend-status session-backend-success" role="status"><CheckCircle2 size={15} />{status}</p> : null}
      {error ? <p className="session-backend-status session-backend-error" role="alert">{error}</p> : null}
      <div className="visual-settings-footer"><span>{dirty ? (zh ? "有未保存的更改" : "Unsaved changes") : (zh ? "当前配置已保存" : "Current configuration is saved")}</span><div><button className="primary" disabled={!dirty || loading || saving} onClick={() => void save()} type="button">{saving ? (zh ? "重启中…" : "Restarting…") : (zh ? "保存并重启" : "Save and restart")}</button></div></div>
    </section>
  </div>;
}
