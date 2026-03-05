import { useState } from "react";
import { HardDrive } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import {
  useClusterStorage,
  useStorageContent,
} from "../api/storage-queries";
import { StorageCapacityBar } from "../components/StorageCapacityBar";
import { StorageContentTable } from "../components/StorageContentTable";
import { UploadDialog } from "../components/UploadDialog";
import type { StorageResponse } from "@/types/api";
import type { StorageContentItem } from "../types/storage";

export function StoragePage() {
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];
  const [selectedClusterId, setSelectedClusterId] = useState<string>("");
  const [selectedPool, setSelectedPool] = useState<StorageResponse | null>(
    null,
  );

  // Auto-select first cluster once loaded.
  const activeClusterId =
    selectedClusterId || (clusters.length > 0 ? clusters[0]?.id ?? "" : "");

  const storageQuery = useClusterStorage(activeClusterId);
  const pools = storageQuery.data ?? [];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <HardDrive className="h-6 w-6 text-primary" />
          <h1 className="text-2xl font-semibold">Storage</h1>
        </div>
      </div>

      {clusters.length > 1 && (
        <div className="flex gap-2">
          {clusters.map((cluster) => (
            <button
              key={cluster.id}
              onClick={() => {
                setSelectedClusterId(cluster.id);
                setSelectedPool(null);
              }}
              className={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
                activeClusterId === cluster.id
                  ? "bg-primary text-primary-foreground"
                  : "bg-muted text-muted-foreground hover:bg-accent"
              }`}
            >
              {cluster.name}
            </button>
          ))}
        </div>
      )}

      {storageQuery.isLoading && (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-36" />
          ))}
        </div>
      )}

      {!selectedPool && !storageQuery.isLoading && (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {pools.map((pool) => (
            <Card
              key={pool.id}
              className="cursor-pointer transition-shadow hover:shadow-md"
              onClick={() => { setSelectedPool(pool); }}
            >
              <CardHeader className="pb-2">
                <div className="flex items-center justify-between">
                  <CardTitle className="text-base">{pool.storage}</CardTitle>
                  <Badge variant={pool.active ? "default" : "secondary"}>
                    {pool.type}
                  </Badge>
                </div>
              </CardHeader>
              <CardContent className="space-y-3">
                <StorageCapacityBar used={pool.used} total={pool.total} />
                <div className="flex flex-wrap gap-1">
                  {pool.content.split(",").map((ct) => (
                    <Badge
                      key={ct}
                      variant="outline"
                      className="text-xs"
                    >
                      {ct.trim()}
                    </Badge>
                  ))}
                </div>
                {pool.shared && (
                  <Badge variant="secondary" className="text-xs">
                    Shared
                  </Badge>
                )}
              </CardContent>
            </Card>
          ))}
          {pools.length === 0 && (
            <p className="col-span-full py-8 text-center text-muted-foreground">
              No storage pools found.
            </p>
          )}
        </div>
      )}

      {selectedPool && (
        <StoragePoolDetail
          pool={selectedPool}
          clusterId={activeClusterId}
          onBack={() => { setSelectedPool(null); }}
        />
      )}
    </div>
  );
}

// --- Storage Pool Detail View ---

interface StoragePoolDetailProps {
  pool: StorageResponse;
  clusterId: string;
  onBack: () => void;
}

function StoragePoolDetail({
  pool,
  clusterId,
  onBack,
}: StoragePoolDetailProps) {
  const contentQuery = useStorageContent(clusterId, pool.id);
  const items = contentQuery.data ?? [];

  const contentTypes = pool.content.split(",").map((s) => s.trim());
  const filterableTypes = contentTypes.filter(
    (t) => t === "iso" || t === "vztmpl" || t === "images" || t === "backup" || t === "rootdir" || t === "snippets",
  );

  // Group items by content type.
  function filterByType(type: string): StorageContentItem[] {
    return items.filter((item) => item.content === type);
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <button
            onClick={onBack}
            className="text-sm text-muted-foreground hover:text-foreground"
          >
            &larr; All Pools
          </button>
          <h2 className="text-xl font-semibold">{pool.storage}</h2>
          <Badge variant="outline">{pool.type}</Badge>
        </div>
        <UploadDialog
          clusterId={clusterId}
          storageId={pool.id}
          supportedContent={pool.content}
        />
      </div>

      <StorageCapacityBar used={pool.used} total={pool.total} />

      {contentQuery.isLoading && (
        <div className="space-y-2">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
      )}

      {!contentQuery.isLoading && filterableTypes.length > 1 && (
        <Tabs defaultValue={filterableTypes[0] ?? "all"}>
          <TabsList>
            {filterableTypes.map((t) => (
              <TabsTrigger key={t} value={t}>
                {t} ({filterByType(t).length})
              </TabsTrigger>
            ))}
            <TabsTrigger value="all">All ({items.length})</TabsTrigger>
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
      )}

      {!contentQuery.isLoading && filterableTypes.length <= 1 && (
        <StorageContentTable
          items={items}
          clusterId={clusterId}
          storageId={pool.id}
        />
      )}

      {contentQuery.isError && (
        <p className="text-sm text-destructive">
          Failed to load storage content.
        </p>
      )}
    </div>
  );
}
