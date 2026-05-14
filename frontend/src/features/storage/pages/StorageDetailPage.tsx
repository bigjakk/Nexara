import { useMemo } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ChevronLeft, Database } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/EmptyState";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useClusterStorage, useStorageContent } from "../api/storage-queries";
import { StorageCapacityBar } from "../components/StorageCapacityBar";
import { StorageContentTable } from "../components/StorageContentTable";
import { StorageGuestTable } from "../components/StorageGuestTable";
import { UploadDialog } from "../components/UploadDialog";
import { BulkMoveDialog } from "../components/BulkMoveDialog";
import { EditStorageDialog } from "../components/EditStorageDialog";
import { DeleteStorageDialog } from "../components/DeleteStorageDialog";
import type { StorageContentItem } from "../types/storage";

export function StorageDetailPage() {
  const { clusterId = "", storageId = "" } = useParams<{
    clusterId: string;
    storageId: string;
  }>();
  const navigate = useNavigate();

  const storageQuery = useClusterStorage(clusterId);
  const nodesQuery = useClusterNodes(clusterId);
  const contentQuery = useStorageContent(clusterId, storageId);

  const pools = useMemo(() => storageQuery.data ?? [], [storageQuery.data]);
  const pool = useMemo(
    () => pools.find((p) => p.id === storageId),
    [pools, storageId],
  );
  const items: StorageContentItem[] = contentQuery.data ?? [];

  const hostNodeName = useMemo(() => {
    if (!pool || pool.shared) return null;
    return nodesQuery.data?.find((n) => n.id === pool.node_id)?.name ?? null;
  }, [nodesQuery.data, pool]);

  const contentTypes = useMemo(
    () => (pool?.content ?? "").split(",").map((s) => s.trim()).filter(Boolean),
    [pool],
  );
  const hasImages = contentTypes.includes("images");
  const hasRootdir = contentTypes.includes("rootdir");
  const hasGuestVolumes = hasImages || hasRootdir;
  const filterableTypes = contentTypes.filter(
    (t) =>
      t === "iso" ||
      t === "vztmpl" ||
      t === "images" ||
      t === "backup" ||
      t === "rootdir" ||
      t === "snippets",
  );

  const guestItems = items.filter(
    (item) => item.content === "images" || item.content === "rootdir",
  );

  const evacuateTargets = useMemo(() => {
    if (!pool) return [];
    const seen = new Set<string>();
    return pools
      .filter(
        (p) =>
          p.content.includes("images") &&
          p.storage !== pool.storage &&
          p.active &&
          p.enabled,
      )
      .filter((p) => {
        if (seen.has(p.storage)) return false;
        seen.add(p.storage);
        return true;
      })
      .map((p) => p.storage);
  }, [pools, pool]);

  const migrateTargets = useMemo(() => {
    if (!pool) return [];
    const seen = new Set<string>();
    return pools
      .filter(
        (p) =>
          p.storage !== pool.storage &&
          p.active &&
          p.enabled &&
          (p.content.includes("images") || p.content.includes("rootdir")),
      )
      .filter((p) => {
        if (seen.has(p.storage)) return false;
        seen.add(p.storage);
        return true;
      })
      .map((p) => p.storage);
  }, [pools, pool]);

  function filterByType(type: string): StorageContentItem[] {
    return items.filter((item) => item.content === type);
  }

  if (storageQuery.isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-4 w-40" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }

  if (!pool) {
    return (
      <EmptyState
        icon={Database}
        title="Storage pool not found"
        description="It may have been removed, or you may not have access."
      />
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <button
            onClick={() => {
              void navigate(`/storage?cluster=${clusterId}`);
            }}
            className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
          >
            <ChevronLeft className="h-4 w-4" />
            All Pools
          </button>
          <Database className="h-6 w-6 text-primary" />
          <h1 className="text-2xl font-semibold">{pool.storage}</h1>
          <Badge variant="outline">{pool.type}</Badge>
          {pool.shared ? (
            <Badge variant="secondary">shared</Badge>
          ) : hostNodeName ? (
            <Badge variant="secondary">on {hostNodeName}</Badge>
          ) : null}
          {!pool.active && <Badge variant="destructive">inactive</Badge>}
        </div>
        <div className="flex items-center gap-2">
          {hasImages && evacuateTargets.length > 0 && (
            <BulkMoveDialog
              clusterId={clusterId}
              storageId={pool.id}
              storageName={pool.storage}
              targetOptions={evacuateTargets}
            />
          )}
          <UploadDialog
            clusterId={clusterId}
            storageId={pool.id}
            supportedContent={pool.content}
          />
          <EditStorageDialog
            clusterId={clusterId}
            storageId={pool.id}
            storageName={pool.storage}
            storageType={pool.type}
          />
          <DeleteStorageDialog
            clusterId={clusterId}
            storageId={pool.id}
            storageName={pool.storage}
            onDeleted={() => {
              void navigate(`/storage?cluster=${clusterId}`);
            }}
          />
        </div>
      </div>

      <Tabs defaultValue="files">
        <TabsList>
          <TabsTrigger value="summary">Summary</TabsTrigger>
          <TabsTrigger value="files">Files</TabsTrigger>
          {hasGuestVolumes && (
            <TabsTrigger value="guests">
              VMs/CTs ({guestItems.length})
            </TabsTrigger>
          )}
        </TabsList>

        <TabsContent value="summary" className="space-y-4">
          <div className="grid gap-4 md:grid-cols-2">
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-base">Capacity</CardTitle>
              </CardHeader>
              <CardContent>
                <StorageCapacityBar used={pool.used} total={pool.total} />
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-base">Details</CardTitle>
              </CardHeader>
              <CardContent>
                <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
                  <dt className="text-muted-foreground">Type</dt>
                  <dd className="font-mono">{pool.type}</dd>
                  <dt className="text-muted-foreground">Shared</dt>
                  <dd>{pool.shared ? "yes" : "no"}</dd>
                  <dt className="text-muted-foreground">Active</dt>
                  <dd>{pool.active ? "yes" : "no"}</dd>
                  <dt className="text-muted-foreground">Enabled</dt>
                  <dd>{pool.enabled ? "yes" : "no"}</dd>
                  {hostNodeName && (
                    <>
                      <dt className="text-muted-foreground">Host node</dt>
                      <dd>{hostNodeName}</dd>
                    </>
                  )}
                </dl>
              </CardContent>
            </Card>
          </div>
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-base">Content Types</CardTitle>
            </CardHeader>
            <CardContent>
              {contentTypes.length === 0 ? (
                <p className="text-xs text-muted-foreground">None declared.</p>
              ) : (
                <div className="flex flex-wrap gap-1">
                  {contentTypes.map((ct) => (
                    <Badge key={ct} variant="outline" className="text-xs">
                      {ct}
                    </Badge>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="files" className="space-y-4">
          {contentQuery.isLoading && (
            <div className="space-y-2">
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </div>
          )}
          {contentQuery.isError && (
            <p className="text-sm text-destructive">
              Failed to load storage content.
            </p>
          )}
          {!contentQuery.isLoading && !contentQuery.isError && (
            filterableTypes.length > 1 ? (
              <Tabs defaultValue={filterableTypes[0] ?? "all"}>
                <TabsList>
                  {filterableTypes.map((t) => (
                    <TabsTrigger key={t} value={t}>
                      {t} ({filterByType(t).length})
                    </TabsTrigger>
                  ))}
                  <TabsTrigger value="all">
                    All ({items.length})
                  </TabsTrigger>
                </TabsList>
                {filterableTypes.map((t) => (
                  <TabsContent key={t} value={t}>
                    <StorageContentTable
                      items={filterByType(t)}
                      clusterId={clusterId}
                      storageId={pool.id}
                    />
                  </TabsContent>
                ))}
                <TabsContent value="all">
                  <StorageContentTable
                    items={items}
                    clusterId={clusterId}
                    storageId={pool.id}
                  />
                </TabsContent>
              </Tabs>
            ) : (
              <StorageContentTable
                items={items}
                clusterId={clusterId}
                storageId={pool.id}
              />
            )
          )}
        </TabsContent>

        {hasGuestVolumes && (
          <TabsContent value="guests">
            {contentQuery.isLoading ? (
              <div className="space-y-2">
                <Skeleton className="h-8 w-full" />
                <Skeleton className="h-8 w-full" />
              </div>
            ) : (
              <StorageGuestTable
                items={guestItems}
                clusterId={clusterId}
                migrateTargets={migrateTargets}
              />
            )}
          </TabsContent>
        )}
      </Tabs>
    </div>
  );
}
