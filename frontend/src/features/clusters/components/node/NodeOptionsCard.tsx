import { useRef, useState } from "react";
import { Pencil, SlidersHorizontal } from "lucide-react";

import {
  QueryFailureNote,
  QueryStateNotice,
} from "@/components/QueryStateNotice";
import { Button } from "@/components/ui/button";
import {
  useNodeOptions,
  type NodeOptions,
} from "../../api/node-options-queries";
import {
  describeBallooningTarget,
  describeLocation,
  describeStartDelay,
  describeWakeOnLan,
  NODE_OPTION_LABELS,
  nodeOptionSupport,
} from "../../lib/node-options";
import { NodeCardShell } from "./NodeCardShell";
import { EditNodeOptionsDialog } from "./NodeOptionsDialog";

/**
 * A node's own settings, as Proxmox's node Options panel lists them: the
 * start-on-boot delay, the ballooning target, Wake-on-LAN and the location. Its
 * notes are the Notes card's, and are read separately.
 *
 * Proxmox forwards the read to the node itself, so an offline node's options
 * can be neither read nor written: the card does not ask, and says so, and
 * keeps showing what it last read. Edit is offered only when the options were
 * read — data, not a successful query: a background refresh that failed keeps
 * the data, and the dialog has all it needs from that — the node is online, and
 * the user may manage nodes. A setting the node's version lacks is not listed,
 * unless the read holds it.
 *
 * Each open mounts a new EditNodeOptionsDialog from a snapshot of `data` (the
 * very object this card rendered), so what the form was drawn from, and the
 * digest a save is pinned to, are what the operator saw when they pressed Edit.
 */
export function NodeOptionsCard({
  clusterId,
  nodeName,
  pveVersion,
  online,
  canEdit,
  className,
}: {
  clusterId: string;
  nodeName: string;
  /** The node's own version, as the node list carries it. */
  pveVersion: string;
  online: boolean;
  canEdit: boolean;
  className?: string;
}) {
  const query = useNodeOptions(clusterId, nodeName, online);
  const data = query.data;
  const [editing, setEditing] = useState<NodeOptions | null>(null);
  const cardRef = useRef<HTMLDivElement>(null);

  return (
    <>
      <NodeCardShell
        icon={<SlidersHorizontal className="h-4 w-4" />}
        title="Options"
        className={className}
        cardRef={cardRef}
        action={
          canEdit && online && data !== undefined ? (
            <Button
              aria-label="Edit node options"
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
        {data === undefined ? (
          online ? (
            <QueryStateNotice
              query={query}
              subject="this node's options"
              empty={null}
            />
          ) : (
            <p className="text-sm text-muted-foreground">
              Options can only be read while the node is online.
            </p>
          )
        ) : (
          <>
            <OptionRows pveVersion={pveVersion} data={data} />
            {!online && (
              <p className="mt-2 text-xs text-muted-foreground">
                Node is offline — showing the options as last read.
              </p>
            )}
            <QueryFailureNote
              query={query}
              subject="this node's options"
              className="mt-2"
            />
          </>
        )}
      </NodeCardShell>
      {editing !== null && (
        <EditNodeOptionsDialog
          clusterId={clusterId}
          nodeName={nodeName}
          pveVersion={pveVersion}
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

function OptionRows({
  pveVersion,
  data,
}: {
  pveVersion: string;
  data: NodeOptions;
}) {
  const support = nodeOptionSupport(pveVersion, data);
  return (
    <dl className="space-y-1.5">
      <Row
        label={NODE_OPTION_LABELS["startall-onboot-delay"]}
        value={describeStartDelay(data["startall-onboot-delay"])}
      />
      {support.ballooningTarget && (
        <Row
          label={NODE_OPTION_LABELS["ballooning-target"]}
          value={describeBallooningTarget(data["ballooning-target"])}
        />
      )}
      <Row
        label={NODE_OPTION_LABELS.wakeonlan}
        value={describeWakeOnLan(data.wakeonlan)}
        title={data.wakeonlan}
      />
      {support.location && (
        <Row
          label={NODE_OPTION_LABELS.location}
          value={describeLocation(data.location)}
          title={data.location}
        />
      )}
    </dl>
  );
}

/**
 * One setting. `title` carries the value as stored when the row shows it in
 * words, or has to cut it short.
 */
function Row({
  label,
  value,
  title,
}: {
  label: string;
  value: string;
  title?: string | undefined;
}) {
  return (
    <div className="flex justify-between gap-2 text-sm">
      <dt className="text-muted-foreground">{label}</dt>
      <dd
        className="min-w-0 truncate text-right font-medium"
        title={title === undefined || title === "" ? value : title}
      >
        {value}
      </dd>
    </div>
  );
}
