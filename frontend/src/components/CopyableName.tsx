import { useEffect, useRef, useState } from "react";
import { Check, Copy, X } from "lucide-react";
import { cn } from "@/lib/utils";

type CopyState = "idle" | "copied" | "failed";

async function copyText(text: string): Promise<boolean> {
  if (window.isSecureContext && "clipboard" in navigator) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Clipboard permission denied — fall through to the legacy path.
    }
  }
  // navigator.clipboard is unavailable over plain HTTP (non-secure context),
  // which is a supported deployment for Nexara, so keep the legacy fallback.
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

interface CopyableNameProps {
  name: string;
  className?: string;
}

/**
 * Inline object name that copies itself to the clipboard on click.
 * Used in type-the-name-to-confirm dialogs so users don't have to
 * select the name by hand. Rendered as span[role=button] (not <button>)
 * so it stays valid inside <label> and lets a wrapping label forward
 * the click to its input after copying.
 */
export function CopyableName({ name, className }: CopyableNameProps) {
  const [copyState, setCopyState] = useState<CopyState>("idle");
  const textRef = useRef<HTMLSpanElement>(null);
  const resetTimer = useRef<number | undefined>(undefined);

  useEffect(() => {
    return () => {
      window.clearTimeout(resetTimer.current);
    };
  }, []);

  function showState(state: CopyState) {
    setCopyState(state);
    window.clearTimeout(resetTimer.current);
    resetTimer.current = window.setTimeout(() => {
      setCopyState("idle");
    }, 2000);
  }

  function handleCopy() {
    void copyText(name).then((ok) => {
      if (ok) {
        showState("copied");
        return;
      }
      // Last resort: select the name so the user can press Ctrl+C.
      const node = textRef.current;
      const selection = window.getSelection();
      if (node && selection) {
        const range = document.createRange();
        range.selectNodeContents(node);
        selection.removeAllRanges();
        selection.addRange(range);
      }
      showState("failed");
    });
  }

  const title =
    copyState === "copied"
      ? "Copied!"
      : copyState === "failed"
        ? "Copy failed — press Ctrl+C to copy the selected text"
        : "Click to copy";

  return (
    <>
      <span
        role="button"
        tabIndex={0}
        title={title}
        onClick={handleCopy}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            handleCopy();
          }
        }}
        className={cn(
          "inline-flex cursor-pointer select-text items-center gap-1 rounded-sm font-semibold text-foreground underline decoration-muted-foreground/50 decoration-dotted underline-offset-[3px] transition-colors hover:decoration-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
          className,
        )}
      >
        <span ref={textRef}>{name}</span>
        {copyState === "copied" ? (
          <Check aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-green-500" />
        ) : copyState === "failed" ? (
          <X aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-destructive" />
        ) : (
          <Copy aria-hidden="true" className="h-3 w-3 shrink-0 text-muted-foreground" />
        )}
      </span>
      <span aria-live="polite" className="sr-only">
        {copyState === "copied" ? "Copied to clipboard" : ""}
      </span>
    </>
  );
}
