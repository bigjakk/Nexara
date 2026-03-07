import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { useDatastoreConfig } from "../api/backup-queries";

interface DatastoreConfigCardProps {
  pbsId: string;
  store: string;
}

export function DatastoreConfigCard({ pbsId, store }: DatastoreConfigCardProps) {
  const { data: config, isLoading } = useDatastoreConfig(pbsId, store);

  if (isLoading) {
    return <Skeleton className="h-40" />;
  }

  if (!config) {
    return null;
  }

  const pruneDefaults = [
    { label: "Keep Last", value: config["keep-last"] },
    { label: "Keep Daily", value: config["keep-daily"] },
    { label: "Keep Weekly", value: config["keep-weekly"] },
    { label: "Keep Monthly", value: config["keep-monthly"] },
    { label: "Keep Yearly", value: config["keep-yearly"] },
  ].filter((d) => d.value != null && d.value > 0);

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">
          {store} Configuration
        </CardTitle>
      </CardHeader>
      <CardContent>
        <div className="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
          {config.path && (
            <div>
              <span className="text-muted-foreground">Path:</span>{" "}
              <span className="font-mono text-xs">{config.path}</span>
            </div>
          )}
          <div>
            <span className="text-muted-foreground">GC Schedule:</span>{" "}
            {config["gc-schedule"] || "Not set"}
          </div>
          <div>
            <span className="text-muted-foreground">Prune Schedule:</span>{" "}
            {config["prune-schedule"] || "Not set"}
          </div>
          <div>
            <span className="text-muted-foreground">Verify New:</span>{" "}
            {config["verify-new"] ? "Yes" : "No"}
          </div>
          {config["maintenance-mode"] && (
            <div>
              <span className="text-muted-foreground">Maintenance:</span>{" "}
              <span className="text-yellow-600">{config["maintenance-mode"]}</span>
            </div>
          )}
          {pruneDefaults.length > 0 && (
            <div className="sm:col-span-2 lg:col-span-3">
              <span className="text-muted-foreground">Prune Defaults:</span>{" "}
              {pruneDefaults.map((d) => `${d.label}: ${String(d.value)}`).join(", ")}
            </div>
          )}
          {config.comment && (
            <div className="sm:col-span-2 lg:col-span-3">
              <span className="text-muted-foreground">Comment:</span>{" "}
              {config.comment}
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
