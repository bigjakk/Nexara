/**
 * Copies text to the clipboard, returning whether it succeeded.
 *
 * The legacy `execCommand` fallback is not dead weight: `navigator.clipboard`
 * is unavailable in a non-secure context, and plain-HTTP is a supported Nexara
 * deployment. Without the fallback, copy silently does nothing for those users
 * — which is worst of all on the one screen where the value is shown exactly
 * once and cannot be retrieved again.
 *
 * Callers that handle a `false` return should offer manual selection (the text
 * in a `select-all` block) rather than just reporting failure.
 */
export async function copyText(text: string): Promise<boolean> {
  if (window.isSecureContext && "clipboard" in navigator) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Clipboard permission denied — fall through to the legacy path.
    }
  }
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  try {
    // eslint-disable-next-line @typescript-eslint/no-deprecated
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    textarea.remove();
  }
}
