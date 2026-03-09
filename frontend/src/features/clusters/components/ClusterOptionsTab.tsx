import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";
import { Copy, Save } from "lucide-react";
import { useAuth } from "@/hooks/useAuth";
import {
  useClusterOptions,
  useClusterDescription, useUpdateClusterDescription,
  useClusterTags, useUpdateClusterTags,
  useClusterJoinInfo, useCorosyncNodes,
} from "../api/cluster-options-queries";

interface ClusterOptionsTabProps {
  clusterId: string;
}

export function ClusterOptionsTab({ clusterId }: ClusterOptionsTabProps) {
  const { canManage } = useAuth();
  const optionsQuery = useClusterOptions(clusterId);
  const descQuery = useClusterDescription(clusterId);
  const tagsQuery = useClusterTags(clusterId);
  const joinQuery = useClusterJoinInfo(clusterId);
  const nodesQuery = useCorosyncNodes(clusterId);
  const updateDesc = useUpdateClusterDescription(clusterId);
  const updateTags = useUpdateClusterTags(clusterId);

  const [description, setDescription] = useState("");
  const [descDirty, setDescDirty] = useState(false);
  const [tagInput, setTagInput] = useState("");
  const [tagAccess, setTagAccess] = useState("");

  const opts = optionsQuery.data;
  const isLoading = optionsQuery.isLoading;

  // Sync description from server
  if (descQuery.data && !descDirty) {
    if (description !== descQuery.data.description) {
      setDescription(descQuery.data.description);
    }
  }

  if (tagsQuery.data && tagInput === "" && tagAccess === "") {
    if (tagsQuery.data.registered_tags) setTagInput(tagsQuery.data.registered_tags);
    if (tagsQuery.data.user_tag_access) setTagAccess(tagsQuery.data.user_tag_access);
  }

  const handleSaveDescription = () => {
    updateDesc.mutate(description, {
      onSuccess: () => { setDescDirty(false); },
    });
  };

  const handleSaveTags = () => {
    const params: { registered_tags?: string; user_tag_access?: string } = {};
    if (tagInput) params.registered_tags = tagInput;
    if (tagAccess) params.user_tag_access = tagAccess;
    updateTags.mutate(params);
  };

  const copyToClipboard = (text: string) => {
    void navigator.clipboard.writeText(text);
  };

  return (
    <Tabs defaultValue="notes">
      <TabsList>
        <TabsTrigger value="notes">Notes</TabsTrigger>
        <TabsTrigger value="general">General</TabsTrigger>
        <TabsTrigger value="tags">Tags</TabsTrigger>
        <TabsTrigger value="info">Cluster Info</TabsTrigger>
      </TabsList>

      <TabsContent value="notes" className="mt-4">
        <Card>
          <CardHeader>
            <CardTitle>Cluster Description / Notes</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <Textarea
              value={description}
              onChange={(e) => { setDescription(e.target.value); setDescDirty(true); }}
              placeholder="Enter cluster notes or description (supports markdown)..."
              rows={8}
              disabled={!canManage("cluster")}
            />
            {canManage("cluster") && (
              <Button
                onClick={handleSaveDescription}
                disabled={!descDirty || updateDesc.isPending}
                size="sm"
              >
                <Save className="mr-2 h-4 w-4" />
                {updateDesc.isPending ? "Saving..." : "Save Description"}
              </Button>
            )}
          </CardContent>
        </Card>
      </TabsContent>

      <TabsContent value="general" className="mt-4">
        <Card>
          <CardHeader>
            <CardTitle>Datacenter Options</CardTitle>
          </CardHeader>
          <CardContent>
            {isLoading ? (
              <div className="space-y-2">
                <Skeleton className="h-8 w-full" />
                <Skeleton className="h-8 w-full" />
              </div>
            ) : opts ? (
              <div className="grid grid-cols-2 gap-4 text-sm">
                <div><Label className="text-muted-foreground">Console</Label><p>{opts.console || "Default"}</p></div>
                <div><Label className="text-muted-foreground">Keyboard</Label><p>{opts.keyboard || "Default"}</p></div>
                <div><Label className="text-muted-foreground">Language</Label><p>{opts.language || "Default"}</p></div>
                <div><Label className="text-muted-foreground">Email From</Label><p>{opts.email_from || "Not set"}</p></div>
                <div><Label className="text-muted-foreground">HTTP Proxy</Label><p>{opts.http_proxy || "None"}</p></div>
                <div><Label className="text-muted-foreground">MAC Prefix</Label><p>{opts.mac_prefix || "Default"}</p></div>
                <div><Label className="text-muted-foreground">Migration Type</Label><p>{opts.migration_type || "Default"}</p></div>
                <div><Label className="text-muted-foreground">Bandwidth Limit</Label><p>{opts.bwlimit || "Unlimited"}</p></div>
                <div><Label className="text-muted-foreground">Next VMID Range</Label><p>{opts["next-id"] || "Auto"}</p></div>
                <div><Label className="text-muted-foreground">Max Workers</Label><p>{opts.max_workers ?? "Default"}</p></div>
                <div><Label className="text-muted-foreground">HA Shutdown Policy</Label><p>{opts.ha || "Default"}</p></div>
                <div><Label className="text-muted-foreground">Fencing</Label><p>{opts.fencing || "Default"}</p></div>
                <div><Label className="text-muted-foreground">CRS</Label><p>{opts.crs || "Default"}</p></div>
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">Failed to load options.</p>
            )}
          </CardContent>
        </Card>
      </TabsContent>

      <TabsContent value="tags" className="mt-4">
        <Card>
          <CardHeader>
            <CardTitle>Tags Management</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label>Registered Tags (semicolon-separated)</Label>
              <Input
                value={tagInput}
                onChange={(e) => { setTagInput(e.target.value); }}
                placeholder="tag1;tag2;tag3"
                disabled={!canManage("cluster")}
              />
            </div>
            <div className="space-y-2">
              <Label>User Tag Access</Label>
              <Input
                value={tagAccess}
                onChange={(e) => { setTagAccess(e.target.value); }}
                placeholder="free, list, existing, or none"
                disabled={!canManage("cluster")}
              />
            </div>
            {tagInput && (
              <div className="flex flex-wrap gap-1">
                {tagInput.split(";").filter(Boolean).map((tag) => (
                  <Badge key={tag} variant="secondary">{tag.trim()}</Badge>
                ))}
              </div>
            )}
            {canManage("cluster") && (
              <Button onClick={handleSaveTags} disabled={updateTags.isPending} size="sm">
                <Save className="mr-2 h-4 w-4" />
                {updateTags.isPending ? "Saving..." : "Save Tags"}
              </Button>
            )}
          </CardContent>
        </Card>
      </TabsContent>

      <TabsContent value="info" className="mt-4 space-y-4">
        <Card>
          <CardHeader>
            <CardTitle>Join Info</CardTitle>
          </CardHeader>
          <CardContent>
            {joinQuery.isLoading ? (
              <Skeleton className="h-16 w-full" />
            ) : joinQuery.data ? (
              <div className="space-y-2 text-sm">
                <div className="flex items-center gap-2">
                  <Label className="text-muted-foreground">Fingerprint</Label>
                  <code className="rounded bg-muted px-2 py-1 text-xs">{joinQuery.data.fingerprint}</code>
                  {joinQuery.data.fingerprint && (
                    <Button variant="ghost" size="sm" onClick={() => { copyToClipboard(joinQuery.data.fingerprint ?? ""); }}>
                      <Copy className="h-3 w-3" />
                    </Button>
                  )}
                </div>
                {joinQuery.data.config_digest && (
                  <div>
                    <Label className="text-muted-foreground">Config Digest</Label>
                    <p className="font-mono text-xs">{joinQuery.data.config_digest}</p>
                  </div>
                )}
              </div>
            ) : (
              <p className="text-sm text-muted-foreground">Join info not available.</p>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Corosync Nodes</CardTitle>
          </CardHeader>
          <CardContent>
            {nodesQuery.isLoading ? (
              <Skeleton className="h-20 w-full" />
            ) : nodesQuery.data && nodesQuery.data.length > 0 ? (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Node ID</TableHead>
                    <TableHead>Address</TableHead>
                    <TableHead>Ring 0</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {nodesQuery.data.map((n) => (
                    <TableRow key={n.name}>
                      <TableCell className="font-medium">{n.name}</TableCell>
                      <TableCell>{n.nodeid}</TableCell>
                      <TableCell>{n.pve_addr}</TableCell>
                      <TableCell>{n.ring0_addr}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            ) : (
              <p className="text-sm text-muted-foreground">No corosync nodes found.</p>
            )}
          </CardContent>
        </Card>
      </TabsContent>
    </Tabs>
  );
}
