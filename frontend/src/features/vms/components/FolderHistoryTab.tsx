import { useMemo } from "react";
import { apiPath } from "@/lib/api-path";
import { HistoryTimeline } from "@/features/history/components/HistoryTimeline";
import type {
  HistoryGuestLink,
  HistoryTarget,
} from "@/features/history/types/history";
import { folderHistoryKind } from "../lib/folder-history-kind";

/**
 * A folder's History tab: what happened to the guests in it and in the
 * folders below it — each guest's History, as on its own page — and to those
 * folders, with a Guest column. folderId is a folder's id, or the
 * "unassigned" segment for Discovered, the guests in no folder. Past its
 * guest cap the server follows the most recently active, and says so, which
 * the timeline shows.
 */
export function FolderHistoryTab({
  clusterId,
  folderId,
  folderNames,
  guestLinks,
}: {
  clusterId: string;
  folderId: string;
  /** The cluster's folders by id, to name the ones a row moves between. */
  folderNames: ReadonlyMap<string, string>;
  /** The cluster's guests by VMID, linked from the Guest column. */
  guestLinks: ReadonlyMap<number, HistoryGuestLink>;
}) {
  const target = useMemo<HistoryTarget>(
    () => ({
      clusterId,
      path: (query) =>
        apiPath`/api/v1/clusters/${clusterId}/vm-folders/${folderId}/history?${query}`,
    }),
    [clusterId, folderId],
  );
  const kind = useMemo(
    () => folderHistoryKind((id) => folderNames.get(id)),
    [folderNames],
  );
  return (
    <HistoryTimeline
      kind={kind}
      target={target}
      guestLink={(vmid) => guestLinks.get(vmid)}
    />
  );
}
