import { AlertTriangle } from "lucide-react";
import { useEffect, useId, useRef, type JSX, type KeyboardEvent } from "react";

type Props = {
  kind: "dirty" | "busy";
  language: "zh" | "en";
  onCancel: () => void;
  onDiscard?: () => void;
};

const FOCUSABLE_SELECTOR = 'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

export function UnsavedChangesDialog({ kind, language, onCancel, onDiscard }: Props): JSX.Element {
  const dialogRef = useRef<HTMLElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const titleID = useId();
  const descriptionID = useId();
  const zh = language === "zh";
  const busy = kind === "busy";

  useEffect(() => {
    previousFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    cancelRef.current?.focus();
    return () => previousFocusRef.current?.focus();
  }, []);

  function handleKeyDown(event: KeyboardEvent<HTMLElement>): void {
    if (event.key === "Escape") {
      event.preventDefault();
      onCancel();
      return;
    }
    if (event.key !== "Tab") return;
    const focusable = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR) ?? []);
    if (focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  return <>
    <button
      aria-label={zh ? "关闭提示" : "Close warning"}
      className="webui2-unsaved-changes-backdrop"
      onClick={onCancel}
      tabIndex={-1}
      type="button"
    />
    <aside
      aria-describedby={descriptionID}
      aria-labelledby={titleID}
      aria-modal="true"
      className="webui2-unsaved-changes-dialog"
      onKeyDown={handleKeyDown}
      ref={dialogRef}
      role="alertdialog"
    >
      <div className="webui2-unsaved-changes-icon" aria-hidden="true"><AlertTriangle size={20} /></div>
      <div className="webui2-unsaved-changes-content">
        <h2 id={titleID}>{busy ? (zh ? "正在保存设置" : "Settings are saving") : (zh ? "有未保存的更改" : "Unsaved changes")}</h2>
        <p id={descriptionID}>{busy
          ? (zh ? "设置正在保存，请稍候完成后再离开。" : "Settings are still being saved. Please wait before leaving.")
          : (zh ? "当前页面有未保存的修改，离开后将丢失。" : "This page has unsaved changes that will be lost if you leave.")}</p>
        <footer>
          <button className="webui2-unsaved-changes-cancel" onClick={onCancel} ref={cancelRef} type="button">{busy ? (zh ? "知道了" : "Got it") : (zh ? "继续编辑" : "Keep editing")}</button>
          {!busy ? <button className="webui2-unsaved-changes-discard" onClick={onDiscard} type="button">{zh ? "放弃修改并返回" : "Discard and leave"}</button> : null}
        </footer>
      </div>
    </aside>
  </>;
}
