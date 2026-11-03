import { FlaskConical, RefreshCcw, Save, Shield, UserPlus } from "lucide-react";
import { useEffect, useState } from "react";
import { listTenantUsers, saveTenantUser } from "../lib/api";
import { useI18n } from "../lib/i18n";
import type { IdentityConfig, TenantUserRecord } from "../lib/types";

type Props = {
  identity: IdentityConfig;
  onChange: (identity: IdentityConfig) => void;
  onCommitIdentity?: (identity: IdentityConfig) => void;
  onSave: () => void;
  onSeedScenario: () => void | Promise<void>;
  onDataChanged?: () => void;
  onStatus?: (message: string) => void;
};

export function ContextPanel({ identity, onChange, onCommitIdentity, onSave, onSeedScenario, onDataChanged, onStatus }: Props) {
  const { t } = useI18n();
  const [users, setUsers] = useState<TenantUserRecord[]>([]);
  const [userKey, setUserKey] = useState(identity.userId);
  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("member");
  const [status, setStatus] = useState("active");
  const [loadingUsers, setLoadingUsers] = useState(false);
  const set = (key: keyof IdentityConfig, value: string) => onChange({ ...identity, [key]: value });

  useEffect(() => {
    setUserKey(identity.userId);
  }, [identity.userId]);

  useEffect(() => {
    void refreshUsers();
  }, [identity.apiBase, identity.apiToken, identity.tenantKey, identity.userId]);

  async function refreshUsers() {
    setLoadingUsers(true);
    try {
      const items = await listTenantUsers(identity);
      setUsers(items);
    } catch (err) {
      onStatus?.(errorMessage(err));
    } finally {
      setLoadingUsers(false);
    }
  }

  async function handleSaveUser() {
    if (userKey.trim() === "") {
      onStatus?.(t("context.userKeyRequired"));
      return;
    }
    try {
      await saveTenantUser(identity, {
        user_key: userKey.trim(),
        display_name: displayName.trim(),
        email: email.trim(),
        role,
        status,
        user_info_json: JSON.stringify({ source: "webui-user-manager", updated_at: new Date().toISOString() })
      });
      await refreshUsers();
      onDataChanged?.();
      onStatus?.(t("context.userSaved", { user: userKey.trim() }));
    } catch (err) {
      onStatus?.(errorMessage(err));
    }
  }

  function selectUser(user: TenantUserRecord) {
    setUserKey(user.user_key || "");
    setDisplayName(user.display_name || "");
    setEmail(user.email || "");
    setRole(user.role || "member");
    setStatus(user.status || "active");
  }

  function switchToUser(user: TenantUserRecord) {
    if (!user.user_key) {
      return;
    }
    const nextIdentity = { ...identity, userId: user.user_key, role: user.role || "member" };
    if (onCommitIdentity) {
      onCommitIdentity(nextIdentity);
    } else {
      onChange(nextIdentity);
    }
    onStatus?.(t("context.userSelected", { user: user.user_key }));
    onDataChanged?.();
  }

  return (
    <section className="panel context-panel">
      <div className="panel-header">
        <div>
          <h2>{t("context.title")}</h2>
          <p>{t("context.subtitle")}</p>
        </div>
        <div className="button-row">
          <button type="button" className="icon-button" onClick={() => void onSeedScenario()} title={t("context.seed")}>
            <FlaskConical size={16} />
          </button>
          <button type="button" className="icon-button" onClick={onSave} title={t("context.save")}>
            <Save size={16} />
          </button>
        </div>
      </div>
      <div className="form-grid two">
        <label>
          {t("context.apiBase")}
          <input value={identity.apiBase} onChange={(event) => set("apiBase", event.target.value)} />
        </label>
        <label>
          {t("app.model")}
          <input value={identity.model} onChange={(event) => set("model", event.target.value)} />
        </label>
        <label>
          {t("app.tenant")}
          <input value={identity.tenantKey} onChange={(event) => set("tenantKey", event.target.value)} />
        </label>
        <label>
          {t("app.user")}
          <input value={identity.userId} onChange={(event) => set("userId", event.target.value)} />
        </label>
        <label>
          {t("context.device")}
          <input value={identity.deviceId} onChange={(event) => set("deviceId", event.target.value)} />
        </label>
        <label>
          {t("context.apiToken")}
          <input value={identity.apiToken} onChange={(event) => set("apiToken", event.target.value)} />
        </label>
      </div>
      <label className="jwt-field">
        {t("context.mobileJwt")}
        <textarea value={identity.mobileJwt} onChange={(event) => set("mobileJwt", event.target.value)} rows={3} />
      </label>
      <div className="notice">
        <Shield size={15} />
        <span>{t("context.notice")}</span>
      </div>
      <section className="context-users">
        <div className="section-heading">
          <div>
            <h3>{t("context.users")}</h3>
            <p>{loadingUsers ? t("chat.loading") : t("chat.loaded", { count: users.length })}</p>
          </div>
          <button type="button" className="icon-button" onClick={() => void refreshUsers()} title={t("context.refreshUsers")}>
            <RefreshCcw size={16} />
          </button>
        </div>
        <div className="context-user-layout">
          <div className="context-user-list">
            {users.length === 0 ? (
              <div className="empty-state compact">{t("context.noUsers")}</div>
            ) : (
              users.map((user) => (
                <div key={user.user_key || user.id} className="record-item context-user-item">
                  <button className="context-user-main" onClick={() => selectUser(user)} type="button">
                    <span>{user.display_name || user.user_key}</span>
                    <small>
                      {user.user_key} · {user.role || "member"} · {user.status || "active"}
                    </small>
                  </button>
                  <button
                    className="inline-action"
                    onClick={() => switchToUser(user)}
                    type="button"
                  >
                    {t("context.switchUser")}
                  </button>
                </div>
              ))
            )}
          </div>
          <div className="form-grid context-user-form">
            <label>
              {t("context.userKey")}
              <input value={userKey} onChange={(event) => setUserKey(event.target.value)} />
            </label>
            <label>
              {t("context.displayName")}
              <input value={displayName} onChange={(event) => setDisplayName(event.target.value)} />
            </label>
            <label>
              {t("context.email")}
              <input value={email} onChange={(event) => setEmail(event.target.value)} />
            </label>
            <label>
              {t("context.role")}
              <select value={role} onChange={(event) => setRole(event.target.value)}>
                <option value="owner">owner</option>
                <option value="admin">admin</option>
                <option value="member">member</option>
              </select>
            </label>
            <label>
              {t("context.userStatus")}
              <select value={status} onChange={(event) => setStatus(event.target.value)}>
                <option value="active">active</option>
                <option value="disabled">disabled</option>
                <option value="archived">archived</option>
              </select>
            </label>
            <button type="button" className="secondary-button" onClick={handleSaveUser}>
              <UserPlus size={15} />
              {t("context.saveUser")}
            </button>
          </div>
        </div>
      </section>
    </section>
  );
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
