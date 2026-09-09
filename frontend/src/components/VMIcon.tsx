import { Container, FileBox, Monitor } from "lucide-react";

/**
 * The glyph for a guest: template, container, or VM.
 *
 * Shared rather than defined per tree — this had already been copied verbatim
 * into both sidebar perspectives, and the favorites list is a third caller that
 * has to agree with them. A guest must not change shape depending on which
 * panel it is drawn in.
 */
export function VMIcon({
  type,
  template,
}: {
  type: string;
  template?: boolean;
}) {
  if (template) {
    return (
      <FileBox className="h-3.5 w-3.5 shrink-0 text-amber-600 dark:text-amber-400" />
    );
  }
  if (type === "lxc") {
    return <Container className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />;
  }
  return <Monitor className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />;
}
