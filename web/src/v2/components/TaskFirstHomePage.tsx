import { FolderOpen, ShieldCheck } from "lucide-react";
import { useEffect, useRef, useState, type JSX } from "react";
import { useI18n } from "../../lib/i18n";
import type { IdentityConfig } from "../../lib/types";
import { useCreateSession, useSendSession } from "../api/sessionControlQueries";
import niumaBrand from "../assets/niuma-animation.svg";
import { Composer } from "./Composer";
import { HOME_PRESETS, WELCOME_DRAFT_REF, type HomePresetID } from "./homeExperience";
import type { OperationResult, SendSessionInput, SessionRef, SessionSummary } from "../types";

type TaskFirstHomePageProps = {
  identity: IdentityConfig;
  availableSources: SessionSummary[];
  defaultWorkspace: string;
  ready: boolean;
  createDisabled?: boolean;
  onCreated: (result: OperationResult) => void;
  onSelectWorkspace?: () => Promise<string | null>;
};

type PendingCreation = {
  fingerprint: string;
  ref: SessionRef;
};

export function TaskFirstHomePage({
  identity,
  availableSources,
  defaultWorkspace,
  ready,
  createDisabled = false,
  onCreated,
  onSelectWorkspace
}: TaskFirstHomePageProps): JSX.Element {
  const { language } = useI18n();
  const create = useCreateSession(identity);
  const send = useSendSession(identity);
  const [selectedPreset, setSelectedPreset] = useState<HomePresetID>("code");
  const [workspace, setWorkspace] = useState(defaultWorkspace);
  const pendingCreation = useRef<PendingCreation | null>(null);
  const selected = HOME_PRESETS.find((preset) => preset.id === selectedPreset) ?? HOME_PRESETS[0];
  const zh = language === "zh";
  const workspaceName = workspace.split(/[\\/]/).filter(Boolean).at(-1) || (zh ? "默认工作区" : "Default workspace");
  const disabled = createDisabled || !ready || !workspace || create.isPending || send.isPending;

  useEffect(() => {
    setWorkspace(defaultWorkspace);
  }, [defaultWorkspace]);

  async function chooseWorkspace(): Promise<void> {
    if (!onSelectWorkspace || disabled) return;
    const selectedWorkspace = await onSelectWorkspace();
    if (selectedWorkspace?.trim()) setWorkspace(selectedWorkspace.trim());
  }

  async function sendFromWelcome(input: SendSessionInput): Promise<OperationResult> {
    const promptMode = selected.promptMode;
    const createKey = `${input.idempotencyKey}:create`;
    const fingerprint = JSON.stringify({
      cwd: workspace,
      promptMode,
      text: input.text.trim(),
      attachments: input.attachments.map((attachment) => [attachment.name, attachment.sha256]),
      sourceRefs: input.sourceRefs
    });
    const hasAdvancedInput = input.attachments.length > 0 || input.sourceRefs.length > 0;
    let created: OperationResult | undefined;
    const existing = pendingCreation.current;

    if (existing?.fingerprint === fingerprint) {
      created = await create.mutateAsync({
        title: titleFor(input.text, selected.title[language]),
        cwd: workspace,
        promptMode,
        idempotencyKey: createKey
      });
    } else if (!hasAdvancedInput) {
      const result = await create.mutateAsync({
        title: titleFor(input.text, selected.title[language]),
        initialText: input.text.trim(),
        cwd: workspace,
        provider: input.provider,
        model: input.model,
        permissionMode: input.permissionMode,
        effort: input.effort,
        promptMode,
        idempotencyKey: createKey
      });
      onCreated(result);
      return result;
    } else {
      created = await create.mutateAsync({
        title: titleFor(input.text, selected.title[language]),
        cwd: workspace,
        provider: input.provider,
        model: input.model,
        permissionMode: input.permissionMode,
        effort: input.effort,
        promptMode,
        idempotencyKey: createKey
      });
      pendingCreation.current = { fingerprint, ref: created.session.ref };
    }

    const result = await send.mutateAsync({
      ...input,
      ref: created.session.ref,
      promptMode,
      idempotencyKey: input.idempotencyKey
    });
    pendingCreation.current = null;
    onCreated(result);
    return result;
  }

  return <section className="webui2-task-first-home">
    <div className="webui2-task-first-heading">
      <img
        alt="niuma"
        className="webui2-task-first-brand-animation"
        decoding="async"
        height="96"
        src={niumaBrand}
        width="240"
      />
      <span className="webui2-task-first-eyebrow">{zh ? "niuma 工作台" : "niuma workbench"}</span>
      <h1>{zh ? "今天想让我帮你做什么？" : "What would you like to work on today?"}</h1>
      <p>{zh ? "从一个任务开始，我会在默认工作区里帮你完成。" : "Start with a task and I will help you finish it in the default workspace."}</p>
    </div>

    <ul aria-label={zh ? "工作方式" : "Ways to work"} className="webui2-home-presets">
      {HOME_PRESETS.map((preset) => {
        const Icon = preset.icon;
        const active = preset.id === selectedPreset;
        return <li key={preset.id}>
          <button
            aria-pressed={active}
            className={active ? "webui2-home-preset is-active" : "webui2-home-preset"}
            onClick={() => setSelectedPreset(preset.id)}
            type="button"
          >
            <Icon aria-hidden="true" size={18} />
            <span><strong>{preset.title[language]}</strong><small>{preset.description[language]}</small></span>
          </button>
        </li>;
      })}
    </ul>

    <div className="webui2-task-first-composer">
      <Composer
        availableSources={availableSources}
        cwd={workspace}
        disabled={disabled}
        drafts={new Map()}
        identity={identity}
        onSend={sendFromWelcome}
        onSent={() => undefined}
        onStopRequested={() => undefined}
        sessionStatus="idle"
        targetRef={WELCOME_DRAFT_REF}
      />
      <div className="webui2-task-first-context">
        <button
          aria-label={zh ? "选择工作区" : "Choose workspace"}
          className="webui2-task-first-context-item"
          disabled={!onSelectWorkspace || disabled}
          onClick={() => void chooseWorkspace()}
          title={workspace}
          type="button"
        >
          <FolderOpen aria-hidden="true" size={16} />
          <span>{zh ? "默认工作区" : "Default workspace"} · {workspaceName}</span>
        </button>
        <span className="webui2-task-first-context-item">
          <ShieldCheck aria-hidden="true" size={16} />
          <span>{zh ? "使用默认权限" : "Default permissions"}</span>
        </span>
      </div>
    </div>

  </section>;
}

function titleFor(text: string, presetTitle: string): string {
  const firstLine = text.trim().split(/\r?\n/, 1)[0]?.trim() ?? "";
  if (!firstLine) return presetTitle;
  return firstLine.length > 48 ? `${firstLine.slice(0, 48)}…` : firstLine;
}
