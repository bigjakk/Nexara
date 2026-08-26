import { AlertTriangle, CheckCircle2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import type { VeeamProbeResult } from "../types/backup";

interface VeeamProbeSummaryProps {
  probe: VeeamProbeResult;
}

function formatDate(value: string): string {
  if (value === "") return "-";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleDateString();
}

/**
 * Renders the result of a connection test.
 *
 * The Proxmox cluster list is the part that matters most: a Veeam server can
 * authenticate, meet the version floor and hold a valid licence while
 * protecting no Proxmox guests at all, and that is a successful connection
 * that will never produce a single row. Saying so here beats an empty
 * dashboard later.
 */
export function VeeamProbeSummary({ probe }: VeeamProbeSummaryProps) {
  return (
    <div className="space-y-3 rounded-md border bg-background p-3">
      <div className="flex items-center gap-2 text-sm font-medium">
        <CheckCircle2 className="h-4 w-4 text-emerald-600 dark:text-emerald-500" />
        Connected to {probe.server_name || "the Veeam server"}
      </div>

      <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2 lg:grid-cols-3">
        <div>
          <dt className="text-xs text-muted-foreground">Build</dt>
          <dd className="font-mono">{probe.build_version}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">API revision</dt>
          <dd className="font-mono">{probe.api_revision}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">Platform</dt>
          <dd>{probe.platform || "-"}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">Licence</dt>
          <dd>
            {probe.license_edition || "-"}
            {probe.license_type !== "" && ` (${probe.license_type})`}
          </dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">Licensed to</dt>
          <dd>{probe.licensed_to || "-"}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">Expires</dt>
          <dd>{formatDate(probe.license_expiration)}</dd>
        </div>
      </dl>

      <div>
        <p className="mb-1 text-xs text-muted-foreground">
          Proxmox clusters covered by this licence
        </p>
        {probe.proxmox_clusters.length === 0 ? (
          <p className="text-sm text-muted-foreground">None</p>
        ) : (
          <div className="flex flex-wrap gap-2">
            {probe.proxmox_clusters.map((cluster) => (
              <Badge key={cluster.name} variant="secondary">
                {cluster.name} · {cluster.vm_count}{" "}
                {cluster.vm_count === 1 ? "VM" : "VMs"}
              </Badge>
            ))}
          </div>
        )}
      </div>

      {probe.warnings.length > 0 && (
        <ul className="space-y-1.5">
          {probe.warnings.map((warning) => (
            <li
              key={warning}
              className="flex gap-2 text-sm text-amber-600 dark:text-amber-500"
            >
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
              <span>{warning}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
