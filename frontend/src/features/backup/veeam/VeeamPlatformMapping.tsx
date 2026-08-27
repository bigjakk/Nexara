import { useState } from "react";
import { AlertTriangle, Link2, Server } from "lucide-react";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { useQuery } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import type { ClusterResponse } from "@/types/api";
import { useMapVeeamPlatform } from "../api/backup-queries";
import type {
  VeeamPlatform,
  VeeamInfrastructureGuest,
} from "../types/backup";

/** Sentinel for "not mapped": Radix Select cannot hold an empty-string value. */
const UNMAPPED = "__unmapped__";

/**
 * Maps each Veeam platform — one Proxmox connection — to a Nexara cluster.
 *
 * This is the first thing an operator has to do after connecting a server, and
 * it is load-bearing for authorization rather than cosmetic. Every
 * cluster-scoped Veeam permission resolves through it: until a platform is
 * mapped, its jobs, sessions and backup objects are unattributable and only a
 * holder of global view:veeam sees any of them. Nothing derives it
 * automatically except the single unambiguous case of a one-cluster install,
 * because Veeam exposes no field naming the Nexara cluster.
 */
export function VeeamPlatformMapping({
  serverId,
  platforms,
  infrastructure,
}: {
  serverId: string;
  platforms: VeeamPlatform[];
  infrastructure: VeeamInfrastructureGuest[];
}) {
  const { data: clusters } = useQuery({
    queryKey: ["clusters"],
    queryFn: () => apiClient.list<ClusterResponse>("/api/v1/clusters"),
  });
  const mapPlatform = useMapVeeamPlatform(serverId);
  // Which row is in flight, so one pending mutation does not freeze the whole
  // table and an error can name the platform it belongs to.
  const [pending, setPending] = useState<string | null>(null);

  const unmapped = platforms.filter((p) => p.cluster_id === null);

  if (platforms.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No Proxmox connections seen yet. They appear once the collector has read
        this server&apos;s backup objects.
      </p>
    );
  }

  return (
    <div className="space-y-4">
      {unmapped.length > 0 && (
        <div className="flex items-start gap-2 rounded-md border border-amber-500/50 bg-amber-500/10 p-3">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
          <p className="text-xs text-amber-600 dark:text-amber-400">
            {unmapped.length === 1
              ? "One connection is not mapped to a cluster. Its backups cannot be matched to guests, they are missing from backup coverage, and only administrators with global Veeam access can see them at all."
              : `${String(unmapped.length)} connections are not mapped to a cluster. Their backups cannot be matched to guests, they are missing from backup coverage, and only administrators with global Veeam access can see them at all.`}
          </p>
        </div>
      )}

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Veeam connection</TableHead>
              <TableHead>Platform ID</TableHead>
              <TableHead className="text-right">Guests</TableHead>
              <TableHead>Nexara cluster</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {platforms.map((platform) => (
              <TableRow key={platform.platform_id}>
                <TableCell className="font-medium">
                  {platform.display_name || (
                    <span className="text-muted-foreground">Unnamed</span>
                  )}
                </TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">
                  {platform.platform_id}
                </TableCell>
                <TableCell className="text-right font-mono text-sm">
                  {platform.object_count}
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <Select
                      value={platform.cluster_id ?? UNMAPPED}
                      onValueChange={(v) => {
                        setPending(platform.platform_id);
                        mapPlatform.mutate(
                          {
                            platformId: platform.platform_id,
                            clusterId: v === UNMAPPED ? null : v,
                          },
                          { onSettled: () => { setPending(null); } },
                        );
                      }}
                      disabled={pending === platform.platform_id}
                    >
                      <SelectTrigger
                        className="w-56"
                        aria-label={`Nexara cluster for ${platform.display_name || platform.platform_id}`}
                      >
                        <SelectValue placeholder="Not mapped" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value={UNMAPPED}>Not mapped</SelectItem>
                        {/*
                          The cluster listing is filtered by view:cluster,
                          while changing this mapping needs only global
                          manage:veeam — so a legitimate operator can be
                          mapped to a cluster that is not in their list, and
                          the Select would fall back to the "Not mapped"
                          placeholder beside a "Mapped" badge. On an
                          authorization-load-bearing control that is the one
                          claim it must never get wrong.
                        */}
                        {platform.cluster_id !== null &&
                          !(clusters ?? []).some((c) => c.id === platform.cluster_id) && (
                            <SelectItem value={platform.cluster_id}>
                              {platform.cluster_name || platform.cluster_id}
                            </SelectItem>
                          )}
                        {clusters?.map((c) => (
                          <SelectItem key={c.id} value={c.id}>
                            {c.name}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    {platform.cluster_id !== null && (
                      <Badge variant="outline" className="gap-1 text-xs">
                        <Link2 className="h-3 w-3" />
                        Mapped
                      </Badge>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {mapPlatform.isError && (
        <p className="text-sm text-destructive">
          Could not change the mapping.{" "}
          {mapPlatform.error instanceof Error ? mapPlatform.error.message : ""}
        </p>
      )}

      <VeeamOwnGuests infrastructure={infrastructure} />
    </div>
  );
}

/**
 * The guests that belong to the Veeam deployment rather than to the workload
 * it protects — worker appliances, and the VBR server when it runs on the
 * cluster it protects.
 *
 * Shown because backup coverage EXCLUDES them, and an exclusion nobody can
 * inspect is indistinguishable from a coverage bug. A row that resolved to no
 * guest is worth seeing too: it means Veeam names a machine Nexara cannot find
 * on any mapped cluster, so it is still being counted as an ordinary
 * unprotected VM.
 */
function VeeamOwnGuests({
  infrastructure,
}: {
  infrastructure: VeeamInfrastructureGuest[];
}) {
  if (infrastructure.length === 0) {
    return null;
  }
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Server className="h-4 w-4 text-muted-foreground" />
        <h4 className="text-sm font-medium">Veeam&apos;s own guests</h4>
      </div>
      <p className="text-xs text-muted-foreground">
        Excluded from backup coverage, and — unless you turn that off in DRS —
        never migrated.
      </p>
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name in Veeam</TableHead>
              <TableHead>Role</TableHead>
              <TableHead>Matched guest</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {infrastructure.map((row) => (
              <TableRow key={row.id}>
                <TableCell className="font-medium">{row.name}</TableCell>
                <TableCell>
                  <Badge variant="outline" className="text-xs">
                    {row.role === "backup_server" ? "Backup server" : "Worker"}
                  </Badge>
                </TableCell>
                <TableCell className="text-sm">
                  {row.vmid === null ? (
                    <span className="text-muted-foreground">
                      No guest on a mapped cluster
                    </span>
                  ) : (
                    <>
                      {row.guest_name || row.name}{" "}
                      <span className="font-mono text-xs text-muted-foreground">
                        ({row.cluster_name} #{row.vmid})
                      </span>
                    </>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
