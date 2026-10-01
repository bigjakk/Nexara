import { useId, useState, type ReactNode } from "react";
import type { QueryObserverResult } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import type { NodeNotes } from "../../api/node-options-queries";
import { useNodeOptionsSave } from "../../hooks/useNodeOptionsSave";
import {
  changedNodeOptions,
  checkNotes,
  descriptionForEditing,
  notesChangedBetween,
  visibleNotes,
  type NodeOptionDrafts,
} from "../../lib/node-options";
import { ConflictNote } from "./ConflictNote";

/**
 * Edits one node's notes (the `description` of its config, which Proxmox shows
 * in the node's Notes panel).
 *
 * Mounted by the card for ONE open and seeded once from `opened`, the notes
 * read it was opened from, as EditNodeOptionsDialog is from the options read.
 * The text starts as it was stored less the one "\n" Proxmox gives every line,
 * so leaving it alone is no change; a text emptied, or left with only
 * whitespace, removes the notes.
 */
export function EditNodeNotesDialog({
  clusterId,
  nodeName,
  opened,
  reread,
  fallbackFocus,
  onClose,
}: {
  clusterId: string;
  nodeName: string;
  opened: NodeNotes;
  reread: () => Promise<QueryObserverResult<NodeNotes>>;
  fallbackFocus: () => HTMLElement | null;
  onClose: () => void;
}) {
  const uid = useId();
  const textId = `${uid}-notes`;
  const errorId = `${uid}-notes-error`;
  const helpId = `${uid}-notes-help`;
  const [text, setText] = useState(descriptionForEditing(opened.description));

  const check = checkNotes(text);
  // The notes alone: the other settings are the Options dialog's, read from a
  // route of their own, and a key added here would not compile.
  const drafts: Pick<NodeOptionDrafts, "description"> = { description: text };
  const changes = check.ok ? changedNodeOptions(opened, drafts) : {};

  const { save, pending, error, conflict, latest } = useNodeOptionsSave({
    clusterId,
    nodeName,
    opened,
    subject: "notes",
    reread,
    onSaved: onClose,
  });
  const found = whatTheRereadFound(opened, latest);

  const canSave =
    check.ok &&
    Object.keys(changes).length > 0 &&
    !pending &&
    conflict !== "rereading";

  const handleSubmit = (event: React.SyntheticEvent) => {
    event.preventDefault();
    if (canSave) save(changes);
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="max-w-xl" fallbackFocus={fallbackFocus}>
        <DialogHeader>
          <DialogTitle>Edit Notes - {nodeName}</DialogTitle>
          <DialogDescription>
            Empty notes are removed from the node's configuration.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} noValidate className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor={textId}>Notes</Label>
            <Textarea
              id={textId}
              rows={12}
              className="font-mono"
              value={text}
              onChange={(event) => {
                setText(event.target.value);
              }}
              spellCheck={false}
              aria-invalid={check.ok ? undefined : true}
              aria-describedby={check.ok ? helpId : `${helpId} ${errorId}`}
            />
            <p id={helpId} className="text-xs text-muted-foreground">
              Proxmox shows these notes as Markdown, links included; Nexara
              shows them as plain text. In Nexara only users who can manage this
              node can read them; in Proxmox anyone with Sys.Audit on / can.
              Keep credentials out.
            </p>
            {!check.ok && (
              <p id={errorId} className="text-xs text-destructive">
                {check.error}
              </p>
            )}
          </div>
          {error !== "" && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
          <ConflictNote
            conflict={conflict}
            repinned={found.sentence}
            details={
              found.stored === null ? null : <StoredNotes text={found.stored} />
            }
          />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={!canSave}>
              {pending ? "Saving..." : "Save"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/**
 * What to say after a 409 whose re-read succeeded. The card that shows the notes
 * now stored on the node is behind the overlay, so when they are not the notes
 * this dialog opened with, and there are any, they are handed back as `stored`
 * to be shown here, read-only: saving replaces them, and what is about to be
 * replaced should be seen first. The `sentence` goes in the live region and
 * `stored` outside it (see ConflictNote).
 */
function whatTheRereadFound(
  was: NodeNotes,
  latest: NodeNotes | null,
): { sentence: ReactNode; stored: string | null } {
  if (latest === null || !notesChangedBetween(was, latest)) {
    return {
      sentence: (
        <p>
          The node's notes are unchanged; something else in its configuration
          changed. Saving again writes the notes shown here.
        </p>
      ),
      stored: null,
    };
  }
  const now = visibleNotes(latest.description);
  if (now === null) {
    return {
      sentence: (
        <p>
          The notes were removed from the node in the meantime. Saving again
          writes the notes shown here.
        </p>
      ),
      stored: null,
    };
  }
  return {
    sentence: <p>Notes now stored on the node — saving replaces them:</p>,
    stored: now,
  };
}

/** The notes now stored on the node, as plain text that cannot be edited. */
function StoredNotes({ text }: { text: string }) {
  return (
    <div
      // Focusable so that notes too long for the box can be scrolled from the
      // keyboard.
      tabIndex={0}
      className="max-h-32 overflow-y-auto whitespace-pre-wrap break-words rounded-md bg-muted px-2 py-1.5 font-mono text-xs"
    >
      {text}
    </div>
  );
}
