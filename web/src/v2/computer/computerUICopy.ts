import type { Language } from "../../lib/i18n";
import type { ComputerCapabilities, ComputerFocusState, ComputerOutcome, ComputerPermissionState, ComputerReadiness, ComputerSessionState, ComputerVerification } from "./types";

const en = {
  nativeTitle: "Computer Use", reopenNativePanel: "Show Computer Use panel",
  workspace: "Computer workspace", controlSurface: "CONTROL SURFACE", launcher: "Open Computer Use workspace", collapseWorkspace: "Collapse Computer Use workspace", dragWorkspace: "Drag Computer Use workspace", collapse: "Collapse", backendDetecting: "detecting", idle: "idle", requested: "requested",
  startSession: "Start session", refreshScreenshot: "Refresh screenshot", pause: "Pause", resume: "Resume", stop: "Stop", refreshRuntimeStatus: "Refresh runtime status", stopRuntime: "Stop runtime", runtimeFaultTitle: "Computer Use runtime needs attention", runtimeFaultNoReplay: "The last action was not replayed. Stop the session before trying a new action.", runtimeRestartHint: "Restart the desktop app after stopping if the runtime does not recover.",
  systemPermissions: "SYSTEM PERMISSIONS", permissionsTitle: "Computer Use needs system permissions", permissionsIntro: "Open the relevant macOS settings, enable access for Computer Use, then recheck permissions.", accessibility: "Accessibility", accessibilityDescription: "Allow Computer Use to control the desktop.", screenRecording: "Screen Recording", screenRecordingDescription: "Allow Computer Use to read the desktop.", openAccessibility: "Open Accessibility settings", openScreenRecording: "Open Screen Recording settings", openingSettings: "Opening settings…", recheckPermissions: "Recheck permissions", checkingPermissions: "Checking permissions…", settingsError: "Unable to open Computer Use permission settings.",
  sessionApproval: "SESSION APPROVAL", approvalTitle: "Allow Computer Use?", approvalDescription: "Do not enter passwords, tokens, or other secrets through Computer Use.", platform: "Platform", capture: "Capture", input: "Input", target: "Target", unknown: "Unknown", unknownBackend: "Unknown backend", currentDesktop: "Current desktop", cancel: "Cancel", starting: "Starting…", approveSession: "Approve session",
  conversationRef: "Conversation ref", modelManagedSession: "Model-managed session (Stop only)", boundConversation: "Desktop access granted to conversation", conversationApprovalDescription: "Approving grants the exact conversation shown below access to observe and operate your desktop. Selecting another conversation does not transfer this grant; stop this session before approving another conversation.",
  localPreviewTitle: "Allow local desktop preview?", localPreview: "Local preview only — no agent access", localPreviewDescription: "No managed conversation is selected. This starts a local desktop preview only. It does NOT allow any agent or conversation to access or control your desktop.", integrationNotice: "This approval does not confirm that autonomous agent execution is connected.",
  previewAria: "Computer preview", liveDesktop: "LIVE DESKTOP", previewTitle: "Computer Preview", controllerPreviewHidden: "Controller window preview hidden", controllerPreviewHiddenDescription: "The controller window preview is hidden to avoid a recursive overlay.", notReady: "not ready", observationAlt: "Desktop observation", screenshotReference: "Screenshot reference received", noObservation: "No observation yet", noDisplayURL: "The backend did not provide a display URL.", startToCapture: "Start an approved session to capture the desktop.", desktop: "Desktop",
  timelineAria: "Computer action timeline", auditTrail: "AUDIT TRAIL", timelineTitle: "Action Timeline", noReceipts: "Actions and before/after receipts will appear here.", computerAction: "Computer action",
  readiness: { unknown: "unknown", unavailable: "unavailable", permission_required: "permission required", ready: "ready", failed: "failed" } satisfies Record<ComputerReadiness, string>,
  permission: { unknown: "unknown", required: "required", approved: "approved", denied: "denied" } satisfies Record<ComputerPermissionState, string>,
  focus: { unknown: "unknown", focused: "focused", changed: "changed", unavailable: "unavailable" } satisfies Record<ComputerFocusState, string>,
  session: { pending_approval: "pending approval", ready: "ready", paused: "paused", needs_observation: "needs observation", stopped: "stopped", failed: "failed" } satisfies Record<ComputerSessionState, string>,
  outcome: { not_started: "not started", executed: "executed", rejected: "rejected", failed: "failed", unknown: "unknown" } satisfies Record<ComputerOutcome, string>,
  verification: { not_checked: "not checked", passed: "passed", failed: "failed", unknown: "unknown" } satisfies Record<ComputerVerification, string>,
  runtimeFault: {
    bridge_disconnected: "The Computer Use runtime connection was lost.",
    worker_exited: "The Computer Use worker exited unexpectedly.",
    helper_unavailable: "The Computer Use helper is unavailable.",
    helper_outcome_uncertain: "The previous Computer Use outcome could not be confirmed.",
    permission_revoked: "Computer Use permissions changed and the session is paused.",
    stop_failed: "The Computer Use runtime did not stop cleanly.",
    runtime_status_unavailable: "The Computer Use runtime status is unavailable.",
    unknown: "The Computer Use runtime reported a safety fault.",
  } satisfies Record<string, string>,
  availability: "Computer Use is unavailable on this host.", checking: "Checking Computer Use capabilities…", captureNotReady: (state: string) => `Screen capture is not ready: ${state}.`, inputNotReady: (state: string) => `Desktop input is not ready: ${state}.`, permissionNotApproved: (state: string) => `System permission is not approved: ${state}.`, imagesNotSupported: "Desktop images are not supported.", stopNotSupported: "The backend must support Stop before a session can start.",
};

const zh: typeof en = {
  nativeTitle: "电脑操作", reopenNativePanel: "显示电脑操作面板",
  workspace: "电脑操作窗口", controlSurface: "控制界面", launcher: "展开电脑操作窗口", collapseWorkspace: "收起电脑操作窗口", dragWorkspace: "拖动电脑操作窗口", collapse: "收起", backendDetecting: "检测中", idle: "空闲", requested: "已请求",
  startSession: "开始会话", refreshScreenshot: "刷新截图", pause: "暂停", resume: "继续", stop: "停止", refreshRuntimeStatus: "刷新运行时状态", stopRuntime: "停止运行时", runtimeFaultTitle: "电脑操作运行时需要处理", runtimeFaultNoReplay: "上一次操作不会被重放。请先停止会话，再尝试新的操作。", runtimeRestartHint: "如果运行时没有恢复，请重启桌面应用。",
  systemPermissions: "系统权限", permissionsTitle: "电脑操作需要系统权限", permissionsIntro: "打开对应的 macOS 设置，为电脑操作开启权限，然后返回此处重新检测。", accessibility: "辅助功能", accessibilityDescription: "允许电脑操作控制桌面。", screenRecording: "屏幕录制", screenRecordingDescription: "允许电脑操作读取桌面画面。", openAccessibility: "打开辅助功能设置", openScreenRecording: "打开屏幕录制设置", openingSettings: "正在打开设置…", recheckPermissions: "重新检测权限", checkingPermissions: "正在检测权限…", settingsError: "无法打开电脑操作权限设置。",
  sessionApproval: "会话授权", approvalTitle: "允许电脑操作？", approvalDescription: "请勿通过电脑操作输入密码、令牌或其他敏感信息。", platform: "平台", capture: "截图", input: "输入", target: "目标", unknown: "未知", unknownBackend: "未知后端", currentDesktop: "当前桌面", cancel: "取消", starting: "正在启动…", approveSession: "授权本次会话",
  conversationRef: "会话引用", modelManagedSession: "模型创建的会话（仅可停止）", boundConversation: "已授权桌面访问的会话", conversationApprovalDescription: "确认后，下方明确显示的会话将获准查看并操作你的桌面。切换所选会话不会转移授权；如需授权其他会话，请先停止本次会话。",
  localPreviewTitle: "允许本地桌面预览？", localPreview: "仅本地预览 — 不授权智能体", localPreviewDescription: "当前未选择托管会话。此操作仅开启本地桌面预览，不允许任何智能体或会话访问或控制你的桌面。", integrationNotice: "此次授权不代表智能体自主执行功能已接通。",
  previewAria: "电脑操作预览", liveDesktop: "实时桌面", previewTitle: "桌面预览", controllerPreviewHidden: "已隐藏控制器窗口预览", controllerPreviewHiddenDescription: "为避免递归叠加，已隐藏控制器窗口预览。", notReady: "未就绪", observationAlt: "桌面观察", screenshotReference: "已收到截图引用", noObservation: "暂无桌面截图", noDisplayURL: "后端未提供可显示的截图地址。", startToCapture: "授权并开始会话后即可截取桌面画面。", desktop: "桌面",
  timelineAria: "电脑操作记录", auditTrail: "执行记录", timelineTitle: "操作记录", noReceipts: "操作及前后截图记录会显示在这里。", computerAction: "电脑操作",
  readiness: { unknown: "未知", unavailable: "不可用", permission_required: "需要授权", ready: "已就绪", failed: "失败" },
  permission: { unknown: "未知", required: "需要授权", approved: "已授权", denied: "已拒绝" },
  focus: { unknown: "未知", focused: "已聚焦", changed: "焦点已变化", unavailable: "不可用" },
  session: { pending_approval: "等待授权", ready: "已就绪", paused: "已暂停", needs_observation: "等待截图", stopped: "已停止", failed: "失败" },
  outcome: { not_started: "未开始", executed: "已执行", rejected: "已拒绝", failed: "失败", unknown: "未知" },
  verification: { not_checked: "未检查", passed: "通过", failed: "失败", unknown: "未知" },
  runtimeFault: {
    bridge_disconnected: "电脑操作运行时连接已断开。",
    worker_exited: "电脑操作工作进程意外退出。",
    helper_unavailable: "电脑操作 helper 当前不可用。",
    helper_outcome_uncertain: "上一次电脑操作结果无法确认。",
    permission_revoked: "电脑操作权限发生变化，会话已暂停。",
    stop_failed: "电脑操作运行时没有正常停止。",
    runtime_status_unavailable: "暂时无法读取电脑操作运行时状态。",
    unknown: "电脑操作运行时报告了安全故障。",
  },
  availability: "此设备暂不支持电脑操作。", checking: "正在检测电脑操作能力…", captureNotReady: (state) => `屏幕截图未就绪：${state}。`, inputNotReady: (state) => `桌面输入未就绪：${state}。`, permissionNotApproved: (state) => `系统权限尚未开通：${state}。`, imagesNotSupported: "此设备不支持桌面截图。", stopNotSupported: "后端必须支持停止操作，才能开始会话。",
};

export const computerUICopy: Record<Language, typeof en> = { en, zh };

export function preferredComputerLanguage(): Language {
  try {
    const stored = window.localStorage.getItem("niuma-webui.language.v1");
    return stored === "en" || stored === "zh" ? stored : "zh";
  } catch {
    return "zh";
  }
}

export type ComputerProgressStatus = "needsAttention" | "pausing" | "stopping" | "working" | "paused" | "waiting" | "awaitingApproval" | "ready";
type ProgressCopy = {
  title: string; liveExecution: string; openProgress: string; closeProgress: string; dragProgress: string; collapse: string; executionSteps: string; stepSummary: string; recentActivity: string; noSteps: string; fallbackAction: string;
  completedSteps: (count: number) => string;
  statusLabel: Record<ComputerProgressStatus, string>;
  statusSummary: Record<ComputerProgressStatus, string>;
};
const enProgress: ProgressCopy = {
  title: "Computer Use progress", liveExecution: "LIVE EXECUTION", openProgress: "Open Computer Use progress", closeProgress: "Close Computer Use progress", dragProgress: "Drag Computer Use execution progress", collapse: "Collapse", executionSteps: "Computer Use execution steps", stepSummary: "STEP SUMMARY", recentActivity: "Recent activity", noSteps: "No action steps have been recorded yet.", fallbackAction: "Computer action", completedSteps: (count) => `${count} ${count === 1 ? "step" : "steps"} completed`,
  statusLabel: { needsAttention: "Needs attention", pausing: "Pausing", stopping: "Stopping", working: "Working", paused: "Paused", waiting: "Waiting", awaitingApproval: "Awaiting approval", ready: "Ready" },
  statusSummary: { needsAttention: "The Computer Use session failed.", pausing: "Pausing the Computer Use session…", stopping: "Stopping the Computer Use session…", working: "Running the current Computer Use step…", paused: "The session is paused. Resume from the workspace when ready.", waiting: "Waiting for the next desktop observation…", awaitingApproval: "Approve the session to start Computer Use.", ready: "Computer Use is ready for the next step." },
};
const zhProgress: ProgressCopy = {
  title: "电脑操作进度", liveExecution: "实时执行", openProgress: "展开电脑操作进度", closeProgress: "收起电脑操作进度", dragProgress: "拖动电脑操作执行进度", collapse: "收起", executionSteps: "电脑操作执行步骤", stepSummary: "步骤摘要", recentActivity: "最近活动", noSteps: "尚未记录操作步骤。", fallbackAction: "电脑操作", completedSteps: (count) => `已完成 ${count} 个步骤`,
  statusLabel: { needsAttention: "需要注意", pausing: "正在暂停", stopping: "正在停止", working: "执行中", paused: "已暂停", waiting: "等待中", awaitingApproval: "等待批准", ready: "已就绪" },
  statusSummary: { needsAttention: "电脑操作会话失败。", pausing: "正在暂停电脑操作会话…", stopping: "正在停止电脑操作会话…", working: "正在执行当前电脑操作步骤…", paused: "会话已暂停，可以从控制面板继续。", waiting: "正在等待下一次桌面观察…", awaitingApproval: "请批准会话后开始电脑操作。", ready: "电脑操作已就绪，可以执行下一步。" },
};
export const computerProgressCopy: Record<Language, typeof en & ProgressCopy> = { en: { ...en, ...enProgress }, zh: { ...zh, ...zhProgress } };

export function computerReadinessMessage(available: boolean, caps: ComputerCapabilities | null, language: Language): string | null {
  const copy = computerUICopy[language];
  if (!available) return copy.availability;
  if (!caps) return copy.checking;
  if (caps.capture_readiness !== "ready") return copy.captureNotReady(copy.readiness[caps.capture_readiness]);
  if (caps.input_readiness !== "ready") return copy.inputNotReady(copy.readiness[caps.input_readiness]);
  if (caps.permission_state !== "approved") return copy.permissionNotApproved(copy.permission[caps.permission_state]);
  if (!caps.image_supported) return copy.imagesNotSupported;
  if (!caps.supports_stop) return copy.stopNotSupported;
  return null;
}

export function computerRuntimeFaultMessage(errorCode: string | undefined, language: Language): string {
  const copy = computerUICopy[language];
  const messages = copy.runtimeFault as Record<string, string>;
  return messages[errorCode ?? "unknown"] ?? messages.unknown;
}

// Preserve unknown native errors verbatim. Only map the client-owned, stable readiness messages.
export function localizeComputerError(message: string, language: Language): string {
  if (language === "zh" && /Screen capture is not ready:\s*([a-z_]+)/i.test(message)) {
    const state = message.match(/Screen capture is not ready:\s*([a-z_]+)/i)?.[1] ?? "unknown";
    return computerUICopy.zh.captureNotReady(computerUICopy.zh.readiness[state as ComputerReadiness] ?? state);
  }
  if (language === "zh" && /Desktop input is not ready:\s*([a-z_]+)/i.test(message)) {
    const state = message.match(/Desktop input is not ready:\s*([a-z_]+)/i)?.[1] ?? "unknown";
    return computerUICopy.zh.inputNotReady(computerUICopy.zh.readiness[state as ComputerReadiness] ?? state);
  }
  if (language === "en") return message;
  for (const key of ["en", "zh"] as const) {
    const source = computerUICopy[key];
    for (const target of ["availability", "checking", "imagesNotSupported", "stopNotSupported"] as const) {
      if (message === source[target]) return computerUICopy[language][target];
    }
    for (const state of Object.keys(source.readiness) as ComputerReadiness[]) {
      if (message === source.captureNotReady(state)) return computerUICopy[language].captureNotReady(computerUICopy[language].readiness[state]);
      if (message === source.inputNotReady(state)) return computerUICopy[language].inputNotReady(computerUICopy[language].readiness[state]);
    }
    for (const state of Object.keys(source.permission) as ComputerPermissionState[]) {
      if (message === source.permissionNotApproved(state)) return computerUICopy[language].permissionNotApproved(computerUICopy[language].permission[state]);
    }
  }
  return message;
}
