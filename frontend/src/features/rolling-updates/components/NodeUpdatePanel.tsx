import { useState } from "react";
import { Link } from "react-router-dom";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Panel } from "@/components/page/Panel";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { useAuth } from "@/hooks/useAuth";
import { PackagePreviewTable } from "./PackagePreviewTable";
import {
  useNodePackagePreview,
  useSSHConfigured,
  useStartInPlaceNodeUpdate,
} from "../api/rolling-update-queries";
import { securityPackageCount } from "../lib/packages";

/**
 * Pending apt updates for one node, and a way to apply them to that node alone.
 *
 * This runs the same machinery the cluster-wide rolling update runs — same SSH
 * path, same job rows, same progress view — with drain_guests off, so the node
 * is upgraded where it stands instead of being emptied first. That is the only
 * shape a single-node cluster can run at all: the drain picks a migration
 * target from the other online nodes and fails the job outright when there
 * isn't one.
 */
export function NodeUpdatePanel({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const [confirmOpen, setConfirmOpen] = useState(false);

  const query = useNodePackagePreview(clusterId, nodeName);
  const { data: packages } = query;

  const { canManage } = useAuth();
  const ssh = useSSHConfigured(clusterId, canManage("rolling_update"));

  const update = useStartInPlaceNodeUpdate();

  const count = packages?.length ?? 0;
  const securityCount = securityPackageCount(packages);

  // The upgrade runs over SSH, so it needs the cluster's stored credentials —
  // the same ones the rolling update needs, configured in Security settings.
  //
  // "Could not check" is kept distinct from "not configured": when the read
  // fails, leave the button enabled and let the create endpoint speak; it
  // returns a precise message if they really are missing, which the app's
  // error toast shows.
  const sshUnknown = ssh.unknown;
  const hasSSH = ssh.configured;
  const allowed = canManage("rolling_update");
  const busy = update.isPending;

  function applyUpdates() {
    update.mutate({ clusterId, nodeName });
  }

  const hasTable = count > 0 && packages !== undefined;

  return (
    <Panel
      flush
      title="Pending updates"
      aside={
        count > 0 ? (
          <>
            {count} {count === 1 ? "package" : "packages"}
            {securityCount > 0 && (
              <span className="text-amber-700 dark:text-amber-400">
                , {securityCount} security
              </span>
            )}
          </>
        ) : undefined
      }
      actions={
        count > 0 && allowed ? (
          <Button
            // Outline: the node page's header holds the screen's primary.
            variant="outline"
            size="sm"
            disabled={(!hasSSH && !sshUnknown) || busy}
            title={
              hasSSH || sshUnknown
                ? undefined
                : "Configure SSH credentials for this cluster first"
            }
            onClick={() => {
              setConfirmOpen(true);
            }}
          >
            {busy && <Loader2 className="mr-2 h-3.5 w-3.5 animate-spin" />}
            Update this node
          </Button>
        ) : undefined
      }
    >
      {/* Rendered in place of the table, never alongside it, so a read that
          failed can never be mistaken for a node with nothing pending — the
          distinction this panel is read for. */}
      {hasTable ? (
        <PackagePreviewTable packages={packages} />
      ) : (
        <div className="px-[18px] pb-1 text-sm">
          <QueryStateNotice
            query={query}
            subject="pending updates"
            empty={
              <span className="flex items-center gap-[7px]">
                <span
                  aria-hidden="true"
                  className="h-2 w-2 shrink-0 rounded-full bg-emerald-500"
                />
                Up to date — no pending packages.
              </span>
            }
          />
        </div>
      )}

      <div className="space-y-2 px-[18px] empty:hidden [&>*:first-child]:pt-3">
        {count > 0 && !hasSSH && !sshUnknown && (
          <p className="text-sm text-muted-foreground">
            Applying updates needs SSH credentials for this cluster. Add them
            under{" "}
            <Link to="/security" className="underline">
              Security
            </Link>
            .
          </p>
        )}

        {update.isSuccess && !busy && (
          <p className="text-sm text-muted-foreground">
            Update started.{" "}
            {/* Deep-linked: /security lands on the vulnerabilities tab by
                default, and the job is what the operator wants to see. */}
            <Link
              to={`/security?tab=rolling-updates&cluster=${update.data.cluster_id}&job=${update.data.id}`}
              className="underline"
            >
              Watch its progress
            </Link>
            .
          </p>
        )}
      </div>

      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Update {nodeName} in place?</AlertDialogTitle>
            {/* Spelled out rather than summarised, because the difference from
                the cluster-wide rolling update is the whole point and is not
                visible from the button. Someone reaching for this on a
                clustered node should be able to tell that they are skipping
                the drain, not just choosing a shorter path to it. */}
            <AlertDialogDescription asChild>
              <div className="space-y-3 text-sm">
                <p>
                  Runs <code>apt dist-upgrade</code> on {nodeName} over SSH,
                  applying {count} pending{" "}
                  {count === 1 ? "package" : "packages"}.
                </p>
                <p>
                  <strong>Guests stay running on this node.</strong> Nothing is
                  migrated and no other node is involved, so this works on a
                  single-node cluster — but the guests are exposed to whatever
                  the upgrade restarts.
                </p>
                <p>
                  A cluster-wide rolling update would instead drain this node
                  first — migrating its guests away and handling HA rules — and
                  only then upgrade it. If this node is part of a cluster and
                  you want that, start a rolling update from Security instead.
                </p>
                <p>
                  Either way, DRS and Proxmox&rsquo;s native auto-rebalancer are
                  paused for the cluster while the update runs, and restored
                  when it finishes.
                </p>
                <p>
                  If the upgrade needs a reboot, the node is{" "}
                  <strong>not</strong> rebooted while guests are running on it.
                  It is flagged as needing one so you can schedule it.
                </p>
              </div>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={applyUpdates}>
              Update in place
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Panel>
  );
}
