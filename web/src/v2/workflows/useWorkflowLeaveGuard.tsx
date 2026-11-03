import { useCallback, useEffect, useRef, useState } from "react";
import { useWorkflowText } from "./i18n";
// Deliberately independent of SettingsCenter's dirty/busy state and dialog.
export function useWorkflowLeaveGuard(enabled: boolean) {
  const dirty = useRef(false);
  const pending = useRef<(() => void) | null>(null);
  const [open, setOpen] = useState(false);
  const dialogRef = useRef<HTMLDialogElement>(null);
  const text = useWorkflowText();
  const setDirty = useCallback((value: boolean) => { dirty.current = value; }, []);
  const request = useCallback((action: () => void) => {
    if (enabled && dirty.current) { pending.current = action; setOpen(true); }
    else action();
  }, [enabled]);
  useEffect(() => {
    const listener = (event: BeforeUnloadEvent) => { if (enabled && dirty.current) { event.preventDefault(); event.returnValue = ""; } };
    window.addEventListener("beforeunload", listener);
    return () => window.removeEventListener("beforeunload", listener);
  }, [enabled]);
  useEffect(() => {
    if (!open) return;
    const previousFocus = document.activeElement;
    const dialog = dialogRef.current;
    if (dialog?.showModal) dialog.showModal();
    else dialog?.setAttribute("open", "");
    return () => { dialog?.close?.(); if (previousFocus instanceof HTMLElement && previousFocus.isConnected) previousFocus.focus(); };
  }, [open]);
  const cancel = () => { pending.current = null; setOpen(false); };
  const discard = () => { const action = pending.current; pending.current = null; dirty.current = false; setOpen(false); action?.(); };
  const dialog = open ? <div className="workflow-leave-backdrop"><dialog ref={dialogRef} className="workflow-leave-dialog" aria-label={text("leave")} onCancel={(event) => { event.preventDefault(); cancel(); }}><p>{text("leave")}</p><div className="workflow-actions"><button onClick={cancel} type="button">{text("stay")}</button><button onClick={discard} type="button">{text("discard")}</button></div></dialog></div> : null;
  return { dirty, setDirty, request, dialog };
}
