import { useRef, useState } from "react";
import { Pencil, StickyNote } from "lucide-react";

import {
  QueryFailureNote,
  QueryStateNotice,
} from "@/components/QueryStateNotice";
import { Button } from "@/components/ui/button";
import {
  notesReadRefusal,
  useNodeNotes,
  type NodeNotes,
} from "../../api/node-options-queries";
import { visibleNotes } from "../../lib/node-options";
import { NodeCardShell } from "./NodeCardShell";
import { EditNodeNotesDialog } from "./NodeNotesDialog";

/**
 * A node's notes, the `description` of its config, which Proxmox shows in the
 * node's Notes panel. Its own read (useNodeNotes), served only to users who can
 * manage the node: they are free text that anyone who can edit the node wrote,
 * and Nexara does not hand them to every viewer. So for anyone else the card
 * makes no request at all and says who can read them; for a manager it follows
 * the Options card's rules for when Edit is offered and for what a dialog
 * opens from.
 *
 * A manager the server refuses with a 403 loses what the card had read, its
 * notes and its Edit: what the server now refuses to give is not for this user
 * to keep reading. What the card then says depends on who refused it
 * (notesReadRefusal). Nexara's own permission check is the viewer's state, no
 * Retry included, since it would be refused again: the permission checked here
 * is flat (canManage names no cluster) and its cache can be a refresh behind the
 * server's, so a user who manages nodes elsewhere, or whose role just changed,
 * is refused. Any other 403, Proxmox's in practice, says nothing about this
 * user; it is a failure like any other, with its own message and a Retry.
 *
 * Shown as PLAIN text, whatever it holds: Proxmox renders these notes as
 * Markdown, but nothing here turns any of it into markup.
 */
export function NodeNotesCard({
  clusterId,
  nodeName,
  online,
  canEdit,
  className,
}: {
  clusterId: string;
  nodeName: string;
  online: boolean;
  /** Whether the user may manage the node: it is what reading the notes takes. */
  canEdit: boolean;
  className?: string;
}) {
  const query = useNodeNotes(clusterId, nodeName, canEdit && online);
  const refusal = notesReadRefusal(query.error);
  // Whether the card is to say who can read the notes, rather than show them or
  // say why it could not: the user's role says they may not, or Nexara's own
  // permission check has just said so.
  const mayRead = canEdit && refusal !== "nexara";
  // A 403 of either kind withdraws what was read: it stays in the cache, and is
  // no longer shown or edited. What shows in its place is the failure notice,
  // which is the data-less state's.
  const data = refusal === null ? query.data : undefined;
  const [editing, setEditing] = useState<NodeNotes | null>(null);
  const cardRef = useRef<HTMLDivElement>(null);
  const notes = visibleNotes(data?.description);

  return (
    <>
      <NodeCardShell
        icon={<StickyNote className="h-4 w-4" />}
        title="Notes"
        className={className}
        cardRef={cardRef}
        action={
          mayRead && online && data !== undefined ? (
            <Button
              aria-label="Edit node notes"
              variant="ghost"
              size="icon"
              className="h-6 w-6"
              onClick={() => {
                setEditing(data);
              }}
            >
              <Pencil className="h-3 w-3" />
            </Button>
          ) : undefined
        }
      >
        {!mayRead ? (
          <p className="text-sm text-muted-foreground">
            Notes are visible to users who can manage this node.
          </p>
        ) : data === undefined ? (
          online ? (
            <QueryStateNotice
              query={query}
              subject="this node's notes"
              empty={null}
            />
          ) : (
            <p className="text-sm text-muted-foreground">
              Notes can only be read while the node is online.
            </p>
          )
        ) : (
          <>
            {notes === null ? (
              <p className="text-sm text-muted-foreground">No notes.</p>
            ) : (
              // Focusable so that a note too long for the box can be scrolled
              // from the keyboard.
              <p
                tabIndex={0}
                className="max-h-48 overflow-y-auto whitespace-pre-wrap break-words text-sm"
              >
                {notes}
              </p>
            )}
            {!online && (
              <p className="mt-2 text-xs text-muted-foreground">
                Node is offline — showing the notes as last read.
              </p>
            )}
            <QueryFailureNote
              query={query}
              subject="this node's notes"
              className="mt-2"
            />
          </>
        )}
      </NodeCardShell>
      {editing !== null && (
        <EditNodeNotesDialog
          clusterId={clusterId}
          nodeName={nodeName}
          opened={editing}
          reread={() => query.refetch()}
          fallbackFocus={() => cardRef.current}
          onClose={() => {
            setEditing(null);
          }}
        />
      )}
    </>
  );
}
