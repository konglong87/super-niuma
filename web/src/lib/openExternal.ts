import { getDesktopServiceBridge } from "../v2/desktopServiceBridge";

// Opens a link in the system browser. Desktop goes through the allowlisted Go
// binding; plain web falls back to window.open. Errors reach the caller.
export async function openExternalURL(url: string): Promise<void> {
  const open = getDesktopServiceBridge()?.OpenExternalURL;
  if (open) {
    await open(url);
    return;
  }
  window.open(url, "_blank", "noopener,noreferrer");
}
