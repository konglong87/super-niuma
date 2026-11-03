import type { DesktopServiceBridge } from "../src/v2/desktopServiceBridge";

declare global {
  interface Window {
    __NIUMA_DESKTOP_TOKEN__?: string;
    go?: {
      main?: {
        app?: DesktopServiceBridge;
        App?: DesktopServiceBridge;
      };
    };
  }
}

export {};
