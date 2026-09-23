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
 *
 * `from`, when given, is a field already on the page that holds `text`, and
 * the legacy path copies from it instead of from a textarea appended to
 * <body>. Inside a modal dialog that matters: the dialog traps focus, and an
 * element outside it that takes focus has it pulled straight back, so the
 * copy would come from wherever the selection ended up.
 */
export async function copyText(
  text: string,
  from?: HTMLTextAreaElement | HTMLInputElement,
): Promise<boolean> {
  if (window.isSecureContext && "clipboard" in navigator) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Clipboard permission denied — fall through to the legacy path.
    }
  }
  if (from) {
    from.focus();
    from.select();
    try {
      // eslint-disable-next-line @typescript-eslint/no-deprecated
      return document.execCommand("copy");
    } catch {
      return false;
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
