import { Info } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

import { useAccessDomains } from "../api/access-queries";

/**
 * Authentication realms, read-only.
 *
 * Creating or editing a realm requires the Realm.Allocate privilege, which
 * Proxmox keeps in its root privilege tier — no built-in role except
 * Administrator carries it, so the recommended PVEAdmin setup would fail. The
 * configuration surface is also large (LDAP/AD/OIDC), so this lists what exists
 * and points at the Proxmox UI for changes rather than half-implementing it.
 */
export function AccessRealmsSection({ clusterId }: { clusterId: string }) {
  const domainsQuery = useAccessDomains(clusterId);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Authentication Realms</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-start gap-2 rounded-md border bg-muted/40 p-3">
          <Info className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
          <p className="text-sm text-muted-foreground">
            Realms are read-only here. Editing them needs the{" "}
            <code className="font-mono">Realm.Allocate</code> privilege, which
            only the Administrator role carries — manage them in the Proxmox UI
            under <em>Datacenter → Permissions → Realms</em>.
          </p>
        </div>

        {domainsQuery.isLoading ? (
          <Skeleton className="h-20 w-full" />
        ) : !domainsQuery.data || domainsQuery.data.length === 0 ? (
          <p className="text-sm text-muted-foreground">No realms found.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Realm</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Two-Factor</TableHead>
                <TableHead>Comment</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {domainsQuery.data.map((domain) => (
                <TableRow key={domain.realm}>
                  <TableCell className="font-medium">
                    <span className="flex items-center gap-2">
                      {domain.realm}
                      {domain.default && (
                        <Badge variant="secondary">Default</Badge>
                      )}
                    </span>
                  </TableCell>
                  <TableCell>
                    <code className="font-mono text-xs">
                      {domain.type || "—"}
                    </code>
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {domain.tfa || "—"}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {domain.comment || "—"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
