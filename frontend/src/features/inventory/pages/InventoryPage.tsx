import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  AlertCircle,
  AlertTriangle,
  Loader2,
  Package,
  Rocket,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { EmptyState } from "@/components/EmptyState";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { useInventoryData } from "../api/inventory-queries";
import { ResourceTable } from "../components/ResourceTable";
import { countInventory } from "../lib/inventory-summary";
import { DeployTemplateDialog } from "@/features/vms/components/DeployTemplateDialog";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { AddClusterDialog } from "@/features/dashboard/components/AddClusterDialog";
import type { ResourceKind } from "@/features/vms/types/vm";

export function InventoryPage() {
  const { t } = useTranslation("inventory");
  const { t: tc } = useTranslation("common");
  const { t: td } = useTranslation("dashboard");
  const { rows, isLoading, error, failedClusterIds } = useInventoryData();
  const { data: clusters } = useClusters();
  const hasClusters = (clusters?.length ?? 0) > 0;
  const failedClusterNames = failedClusterIds
    .map((id) => clusters?.find((c) => c.id === id)?.name ?? id.slice(0, 8))
    .join(", ");

  const [deployTarget, setDeployTarget] = useState<{
    clusterId: string;
    vmId: string;
    kind: ResourceKind;
    name: string;
  } | null>(null);

  const resourceRows = rows.filter((r) => !r.template);
  const templateRows = rows.filter((r) => r.template);

  // What the estate holds, in one sentence. Only a node that is down is
  // worth a clause of its own.
  const counts = countInventory(rows);
  const summary =
    rows.length === 0
      ? t("browseAllResources")
      : [
          `${t("summaryGuests", { count: counts.guests })} ${t("summaryOnNodes", { count: counts.nodes })}`,
          t("summaryRunning", { count: counts.running }),
          ...(counts.templates > 0
            ? [t("summaryTemplates", { count: counts.templates })]
            : []),
          ...(counts.nodesOffline > 0
            ? [t("summaryNodesOffline", { count: counts.nodesOffline })]
            : []),
        ].join(", ");

  return (
    <div className="space-y-5">
      <PageHeader title={t("inventory")} summary={summary} />

      {isLoading && (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
        </div>
      )}

      {error && (
        <div className="flex items-center gap-2 rounded-[10px] bg-destructive/10 px-[18px] py-3 text-sm text-destructive">
          <AlertCircle className="h-4 w-4 shrink-0" />
          {t("failedLoadInventory")}
        </div>
      )}

      {!isLoading && !error && failedClusterIds.length > 0 && (
        <div className="flex items-center gap-2 rounded-[10px] bg-amber-500/10 px-[18px] py-3 text-sm text-amber-700 dark:text-amber-400">
          <AlertTriangle className="h-4 w-4 shrink-0" />
          {t("partialInventoryWarning", { clusters: failedClusterNames })}
        </div>
      )}

      {!isLoading &&
        !error &&
        rows.length === 0 &&
        failedClusterIds.length === 0 &&
        (hasClusters ? (
          <EmptyState
            icon={Package}
            title={t("noResources")}
            description={t("noResourcesDescription")}
          />
        ) : (
          <EmptyState
            icon={Package}
            title={td("noClustersRegistered")}
            description={td("addClusterToGetStarted")}
            action={<AddClusterDialog />}
          />
        ))}

      {!isLoading && !error && rows.length > 0 && (
        <Tabs defaultValue="resources">
          <TabsList>
            <TabsTrigger value="resources">
              {t("resourcesCount", { count: resourceRows.length })}
            </TabsTrigger>
            <TabsTrigger value="templates">
              {t("templatesCount", { count: templateRows.length })}
            </TabsTrigger>
          </TabsList>

          <TabsContent value="resources" className="mt-5">
            <ResourceTable data={resourceRows} label={t("resources")} />
          </TabsContent>

          <TabsContent value="templates" className="mt-5">
            {templateRows.length === 0 ? (
              <EmptyState
                icon={Package}
                title={t("noTemplatesTitle")}
                description={t("noTemplates")}
              />
            ) : (
              <Panel flush aria-label={t("templates")}>
                <Table>
                  <TableHeader>
                    <TableRow className="hover:bg-transparent">
                      <TableHead>{tc("name")}</TableHead>
                      <TableHead>{tc("type")}</TableHead>
                      <TableHead>VMID</TableHead>
                      <TableHead>{t("cluster", { ns: "audit" })}</TableHead>
                      <TableHead>{t("node")}</TableHead>
                      <TableHead className="text-right">
                        <span className="sr-only">{tc("actions")}</span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {templateRows.map((row) => (
                      <TableRow key={row.key}>
                        <TableCell className="py-2 font-medium">
                          {row.name}
                        </TableCell>
                        <TableCell className="py-2 text-[12.5px] text-muted-foreground">
                          {row.type === "ct" ? "CT" : "VM"}
                        </TableCell>
                        <TableCell className="py-2 tabular-nums text-muted-foreground">
                          {row.vmid !== null ? String(row.vmid) : "—"}
                        </TableCell>
                        <TableCell className="py-2 text-muted-foreground">
                          {row.clusterName}
                        </TableCell>
                        <TableCell className="py-2 text-muted-foreground">
                          {row.nodeName}
                        </TableCell>
                        <TableCell className="py-1.5 text-right">
                          <Button
                            size="sm"
                            variant="outline"
                            className="gap-1.5"
                            onClick={() => {
                              setDeployTarget({
                                clusterId: row.clusterId,
                                vmId: row.id,
                                kind: row.type as ResourceKind,
                                name: row.name,
                              });
                            }}
                          >
                            <Rocket />
                            {t("deploy", { ns: "vms" })}
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </Panel>
            )}
          </TabsContent>
        </Tabs>
      )}

      {deployTarget && (
        <DeployTemplateDialog
          open={true}
          onOpenChange={(open) => {
            if (!open) setDeployTarget(null);
          }}
          clusterId={deployTarget.clusterId}
          vmId={deployTarget.vmId}
          kind={deployTarget.kind}
          templateName={deployTarget.name}
        />
      )}
    </div>
  );
}
