import { vi } from "vitest";

/**
 * jsdom has no URL.createObjectURL. This stubs the global URL with a subclass
 * that supplies it and revokeObjectURL, while `new URL` keeps working, and
 * records each blob it was given. Undo with vi.unstubAllGlobals().
 */
export function stubObjectUrls() {
  const blobs: Blob[] = [];
  class RecordingURL extends URL {
    static override createObjectURL(blob: Blob): string {
      blobs.push(blob);
      return `blob:test/${String(blobs.length)}`;
    }
    static override revokeObjectURL(): void {
      // nothing to release
    }
  }
  vi.stubGlobal("URL", RecordingURL);
  return { blobs };
}
