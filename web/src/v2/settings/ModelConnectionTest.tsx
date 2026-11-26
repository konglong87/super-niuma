import { Activity } from "lucide-react";
import type { GlobalSettingsDraft } from "./globalSettingsDraft";
import { useModelSettingsText } from "./modelSettingsCopy";

export function ProviderConnection({ draft, provider }: { draft: GlobalSettingsDraft; provider?: string }) {
  const t = useModelSettingsText();
  const unnamed = provider !== undefined && !provider.trim();
  const connection = draft.connection?.provider === provider ? draft.connection : null;
  return <div className="model-connection-test"><p>{t("测试会检查模型目录，并发送一次最小对话验证 Key 和模型，不验证工具或看图能力。")}</p><div className="global-settings-connection"><span role="status">{unnamed ? t("请先填写供应商名称") : connection ? `${t(connection.kind === "chat_connection" ? (connection.ok ? "对话测试成功" : "对话测试失败") : connection.ok ? "模型目录连接成功" : "模型目录连接失败")}: ${connection.message}` : t("尚未测试模型目录连接")}</span><button type="button" disabled={unnamed || draft.busy || !draft.doc || Boolean(draft.syntaxError)} onClick={() => void draft.testProvider(provider)}><Activity size={15} />{t(draft.operation === "test" ? "测试中…" : "测试连接")}</button></div></div>;
}
