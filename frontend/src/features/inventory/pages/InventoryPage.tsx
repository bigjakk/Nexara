import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertCircle, Loader2, Package, Rocket } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useInventoryData } from "../api/inventory-queries";
import { ResourceTable } from "../components/ResourceTable";
import { DeployTemplateDialog } from "@/features/vms/components/DeployTemplateDialog";
import type { ResourceKind } from "@/features/vms/types/vm";

export function InventoryPage() {
  const { t } = useTranslation("inventory");
  const { t: tc } = useTranslation("common");
  const { rows, isLoading, error } = useInventoryData();

  const [deployTarget, setDeployTarget] = useState<{
    clusterId: string;
    vmId: string;
    kind: ResourceKind;
    name: string;
  } | null>(null);

  const resourceRows = rows.filter((r) => !r.template);
  const templateRows = rows.filter((r) => r.template);

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("inventory")}</h1>
          <p className="text-sm text-muted-foreground">
            {t("browseAllResources")}
          </p>
        </div>
      </div>

      {isLoading && (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
        </div>
      )}

      {error && (
        <div className="flex items-center gap-2 rounded-lg border border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          <AlertCircle className="h-4 w-4 shrink-0" />
          {t("failedLoadInventory")}
        </div>
      )}

      {!isLoading && !error && rows.length === 0 && (
        <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <Package className="mb-2 h-10 w-10" />
          <p className="text-sm">{t("noResources")}</p>
        </div>
      )}

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

          <TabsContent value="resources" className="mt-4">
            <ResourceTable data={resourceRows} />
          </TabsContent>

          <TabsContent value="templates" className="mt-4">
            {templateRows.length === 0 ? (
              <p className="py-8 text-center text-sm text-muted-foreground">
                {t("noTemplates")}
              </p>
            ) : (
              <div className="overflow-hidden rounded-lg border">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b bg-muted/50">
                      <th className="px-4 py-2 text-left font-medium">{tc("type")}</th>
                      <th className="px-4 py-2 text-left font-medium">{tc("name")}</th>
                      <th className="px-4 py-2 text-left font-medium">VMID</th>
                      <th className="px-4 py-2 text-left font-medium">{t("cluster", { ns: "audit" })}</th>
                      <th className="px-4 py-2 text-left font-medium">{t("node")}</th>
                      <th className="px-4 py-2 text-right font-medium">{tc("actions")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {templateRows.map((row) => (
                      <tr key={row.key} className="border-b last:border-b-0">
                        <td className="px-4 py-2 uppercase text-muted-foreground">{row.type}</td>
                        <td className="px-4 py-2 font-medium">{row.name}</td>
                        <td className="px-4 py-2 text-muted-foreground">
                          {row.vmid !== null ? String(row.vmid) : "--"}
                        </td>
                        <td className="px-4 py-2 text-muted-foreground">{row.clusterName}</td>
                        <td className="px-4 py-2 text-muted-foreground">{row.nodeName}</td>
                        <td className="px-4 py-2 text-right">
                          <Button
                            size="sm"
                            variant="outline"
                            className="gap-1"
                            onClick={() => {
                              setDeployTarget({
                                clusterId: row.clusterId,
                                vmId: row.id,
                                kind: row.type as ResourceKind,
                                name: row.name,
                              });
                            }}
                          >
                            <Rocket className="h-3.5 w-3.5" />
                            {t("deploy", { ns: "vms" })}
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </TabsContent>
        </Tabs>
      )}

      {deployTarget && (
        <DeployTemplateDialog
          open={true}
          onOpenChange={(open) => { if (!open) setDeployTarget(null); }}
          clusterId={deployTarget.clusterId}
          vmId={deployTarget.vmId}
          kind={deployTarget.kind}
          templateName={deployTarget.name}
        />
      )}
    </div>
  );
}
