import type { ReactNode } from "react";

import { Panel, PropertyList, PropertyRow } from "@/components/page/Panel";
import { formatUptime } from "@/lib/format";
import type { NodeResponse } from "@/types/api";
import {
  DNSReadNote,
  EditDNSDialog,
  EditTimezoneDialog,
  TimezoneReadNote,
} from "./NodeSettingsDialogs";

/**
 * A value that is cut short when it is long, with all of it on hover, and
 * beside it the button that edits it.
 */
function Value({ children, edit }: { children: string; edit?: ReactNode }) {
  return (
    <span className="flex min-w-0 items-center justify-end gap-1">
      <span className="min-w-0 truncate" title={children}>
        {children}
      </span>
      {edit}
    </span>
  );
}

/**
 * The software the node runs and how it is set up, as the node list reports
 * them, with the two settings that can be changed from here: its timezone and
 * its DNS.
 *
 * `canEdit` is who is shown the timezone and DNS dialogs, and who makes the
 * reads they edit from. It gates what is DRAWN, not a flag the dialogs take:
 * they then unmount with the permission (or the node going offline), their
 * state with them, and nothing comes back from an old snapshot when it
 * returns.
 */
export function NodeSystemPanel({
  clusterId,
  node,
  canEdit,
}: {
  clusterId: string;
  node: NodeResponse;
  canEdit: boolean;
}) {
  return (
    <Panel title="System">
      <PropertyList>
        <PropertyRow label="PVE version">
          <Value>{node.pve_version || "—"}</Value>
        </PropertyRow>
        <PropertyRow label="Kernel">
          <Value>{node.kernel_version || "—"}</Value>
        </PropertyRow>
        <PropertyRow label="Uptime">{formatUptime(node.uptime)}</PropertyRow>
        {/* The timezone and DNS dialogs are keyed on the node, like the
            Options and Notes panels: the page is the same component for
            every node it is routed to, and a dialog that survived a change
            of node would PUT what it was opened with to the new one. */}
        <PropertyRow label="Timezone">
          <Value
            edit={
              canEdit ? (
                <EditTimezoneDialog
                  key={`timezone-${node.id}`}
                  clusterId={clusterId}
                  nodeName={node.name}
                />
              ) : undefined
            }
          >
            {node.timezone || "—"}
          </Value>
        </PropertyRow>
        <PropertyRow label="DNS servers">
          <Value
            edit={
              canEdit ? (
                <EditDNSDialog
                  key={`dns-${node.id}`}
                  clusterId={clusterId}
                  nodeName={node.name}
                />
              ) : undefined
            }
          >
            {node.dns_servers || "—"}
          </Value>
        </PropertyRow>
        <PropertyRow label="Search domain">
          <Value>{node.dns_search || "—"}</Value>
        </PropertyRow>
        {node.subscription_status && (
          <PropertyRow label="Subscription">
            <Value>
              {`${node.subscription_status}${node.subscription_level ? ` (${node.subscription_level})` : ""}`}
            </Value>
          </PropertyRow>
        )}
      </PropertyList>
      {/* Under the rows: what could not be read, and so cannot be edited. */}
      {canEdit && (
        <>
          <TimezoneReadNote clusterId={clusterId} nodeName={node.name} />
          <DNSReadNote clusterId={clusterId} nodeName={node.name} />
        </>
      )}
    </Panel>
  );
}
