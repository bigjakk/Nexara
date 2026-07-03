// deriveFilenameFromURL extracts a safe download filename from a URL's path basename,
// or "" when none can be derived (unparseable URL, or a path ending in a slash). The
// backend and the Proxmox client re-validate the final filename before use.
export function deriveFilenameFromURL(input: string): string {
  try {
    const u = new URL(input);
    const segments = u.pathname.split("/").filter(Boolean);
    const last = segments.length > 0 ? segments[segments.length - 1] : "";
    return last ?? "";
  } catch {
    return "";
  }
}
