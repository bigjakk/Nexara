import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { apiPath } from "@/lib/api-path";
import { HistoryTimeline } from "@/features/history/components/HistoryTimeline";
import type {
  HistoryGuestLink,
  HistoryTarget,
} from "@/features/history/types/history";
import {
  FOLDER_HISTORY_GUEST_CAP,
  folderHistoryKind,
} from "../lib/folder-history-kind";

/**
 * A folder's History tab: what happened to the guests in it and in the
 * folders below it — each guest's History, as on its own page — and to those
 * folders, with a Guest column. folderId is a folder's id, or the
 * "unassigned" segment for Discovered, the guests in no folder. Past
 * FOLDER_HISTORY_GUEST_CAP guests the server follows the lowest VMIDs, which
 * the tab says.
 */
export function FolderHistoryTab({
  clusterId,
  folderId,
  guestCount,
  folderNames,
  guestLinks,
}: {
  clusterId: string;
  folderId: string;
  /** How many guests the folder holds, as the page counts them. */
  guestCount: number;
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
  const { t } = useTranslation("history-folder");
  return (
    <div className="space-y-3">
      {guestCount > FOLDER_HISTORY_GUEST_CAP && (
        <p className="text-sm text-muted-foreground">
          {t("capped", {
            count: guestCount,
            guests: guestCount.toLocaleString(),
            cap: FOLDER_HISTORY_GUEST_CAP.toLocaleString(),
          })}
        </p>
      )}
      <HistoryTimeline
        kind={kind}
        target={target}
        guestLink={(vmid) => guestLinks.get(vmid)}
      />
    </div>
  );
}
