import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";
import { RefreshCw, Trash2 } from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import { useACMEAccounts, useACMEPlugins, useDeleteACMEAccount, useDeleteACMEPlugin } from "@/features/acme/api/acme-queries";
import { useClusterNodes } from "../api/cluster-queries";
import { useNodeCertificates, useRenewNodeCertificate } from "@/features/acme/api/acme-queries";
import { useState } from "react";

interface ClusterACMETabProps {
  clusterId: string;
}

export function ClusterACMETab({ clusterId }: ClusterACMETabProps) {
  const { canManage } = useAuth();
  const accountsQuery = useACMEAccounts(clusterId);
  const pluginsQuery = useACMEPlugins(clusterId);
  const deleteAccount = useDeleteACMEAccount(clusterId);
  const deletePlugin = useDeleteACMEPlugin(clusterId);
  const nodesQuery = useClusterNodes(clusterId);
  const [selectedNode, setSelectedNode] = useState("");
  const renewCert = useRenewNodeCertificate(clusterId);

  const firstNode = nodesQuery.data?.[0]?.name ?? "";
  const certNode = selectedNode || firstNode;
  const certsQuery = useNodeCertificates(clusterId, certNode);

  const formatDate = (ts?: number) => {
    if (!ts) return "—";
    return new Date(ts * 1000).toLocaleDateString();
  };

  return (
    <Tabs defaultValue="accounts">
      <TabsList>
        <TabsTrigger value="accounts">Accounts</TabsTrigger>
        <TabsTrigger value="plugins">Plugins</TabsTrigger>
        <TabsTrigger value="certificates">Node Certificates</TabsTrigger>
      </TabsList>

      <TabsContent value="accounts" className="mt-4">
        <Card>
          <CardHeader><CardTitle>ACME Accounts</CardTitle></CardHeader>
          <CardContent>
            {accountsQuery.isLoading ? <Skeleton className="h-20 w-full" /> :
             !accountsQuery.data || accountsQuery.data.length === 0 ? (
              <p className="text-sm text-muted-foreground">No ACME accounts configured.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Directory</TableHead>
                    {canManage("certificate") && <TableHead className="text-right">Actions</TableHead>}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {accountsQuery.data.map((acc) => (
                    <TableRow key={acc.name ?? "default"}>
                      <TableCell className="font-medium">{acc.name ?? "default"}</TableCell>
                      <TableCell className="text-xs">{acc.directory}</TableCell>
                      {canManage("certificate") && (
                        <TableCell className="text-right">
                          <Button variant="ghost" size="sm" onClick={() => { deleteAccount.mutate(acc.name ?? "default"); }}>
                            <Trash2 className="h-4 w-4 text-destructive" />
                          </Button>
                        </TableCell>
                      )}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </TabsContent>

      <TabsContent value="plugins" className="mt-4">
        <Card>
          <CardHeader><CardTitle>ACME Plugins</CardTitle></CardHeader>
          <CardContent>
            {pluginsQuery.isLoading ? <Skeleton className="h-20 w-full" /> :
             !pluginsQuery.data || pluginsQuery.data.length === 0 ? (
              <p className="text-sm text-muted-foreground">No ACME plugins configured.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Plugin</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead>API</TableHead>
                    {canManage("certificate") && <TableHead className="text-right">Actions</TableHead>}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {pluginsQuery.data.map((p) => (
                    <TableRow key={p.plugin}>
                      <TableCell className="font-medium">{p.plugin}</TableCell>
                      <TableCell><Badge variant="outline">{p.type}</Badge></TableCell>
                      <TableCell className="text-xs">{p.api ?? "—"}</TableCell>
                      {canManage("certificate") && (
                        <TableCell className="text-right">
                          <Button variant="ghost" size="sm" onClick={() => { deletePlugin.mutate(p.plugin); }}>
                            <Trash2 className="h-4 w-4 text-destructive" />
                          </Button>
                        </TableCell>
                      )}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </TabsContent>

      <TabsContent value="certificates" className="mt-4">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between">
            <CardTitle>Node Certificates</CardTitle>
            {nodesQuery.data && nodesQuery.data.length > 1 && (
              <select
                className="rounded border bg-background px-2 py-1 text-sm"
                value={certNode}
                onChange={(e) => { setSelectedNode(e.target.value); }}
              >
                {nodesQuery.data.map((n) => (
                  <option key={n.name} value={n.name}>{n.name}</option>
                ))}
              </select>
            )}
          </CardHeader>
          <CardContent>
            {certsQuery.isLoading ? <Skeleton className="h-20 w-full" /> :
             !certsQuery.data || certsQuery.data.length === 0 ? (
              <p className="text-sm text-muted-foreground">No certificates found.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>File</TableHead>
                    <TableHead>Subject</TableHead>
                    <TableHead>Issuer</TableHead>
                    <TableHead>Valid Until</TableHead>
                    {canManage("certificate") && <TableHead className="text-right">Actions</TableHead>}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {certsQuery.data.map((cert) => (
                    <TableRow key={cert.filename}>
                      <TableCell className="font-medium text-xs">{cert.filename}</TableCell>
                      <TableCell className="text-xs">{cert.subject}</TableCell>
                      <TableCell className="text-xs">{cert.issuer}</TableCell>
                      <TableCell>{formatDate(cert.notafter)}</TableCell>
                      {canManage("certificate") && (
                        <TableCell className="text-right">
                          <Button variant="ghost" size="sm" onClick={() => { renewCert.mutate({ node: certNode }); }} title="Renew">
                            <RefreshCw className="h-4 w-4" />
                          </Button>
                        </TableCell>
                      )}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </TabsContent>
    </Tabs>
  );
}
