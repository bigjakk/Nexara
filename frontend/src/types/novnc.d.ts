declare module "@novnc/novnc/lib/rfb" {
  interface RFBOptions {
    shared?: boolean;
    credentials?: { password?: string; target?: string; username?: string };
    wsProtocols?: string[];
  }

  export default class RFB extends EventTarget {
    constructor(
      target: HTMLElement,
      urlOrChannel: string | WebSocket,
      options?: RFBOptions,
    );

    scaleViewport: boolean;
    resizeSession: boolean;
    clipViewport: boolean;
    showDotCursor: boolean;
    background: string;
    qualityLevel: number;
    compressionLevel: number;
    viewOnly: boolean;
    focusOnClick: boolean;

    disconnect(): void;
    sendCredentials(credentials: {
      password?: string;
      target?: string;
      username?: string;
    }): void;
    sendKey(keysym: number, code: string | null, down?: boolean): void;
    sendCtrlAltDel(): void;
    focus(): void;
    blur(): void;
    machineShutdown(): void;
    machineReboot(): void;
    machineReset(): void;
    clipboardPasteFrom(text: string): void;
  }
}
