import { useState } from "react";
import { ShieldAlert, Loader2, RefreshCw } from "lucide-react";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import {
  useCVEScans,
  useSecurityPosture,
  useTriggerScan,
} from "../api/cve-queries";
import { SecurityPostureCard } from "../components/SecurityPostureCard";
import { ScanScheduleCard } from "../components/ScanScheduleCard";
import { ScanHistoryTable } from "../components/ScanHistoryTable";
import { VulnerabilityTable } from "../components/VulnerabilityTable";

export function SecurityDashboardPage() {
  const { data: clusters, isLoading: clustersLoading } = useClusters();
  const [selectedClusterId, setSelectedClusterId] = useState<string>("");
  const [selectedScanId, setSelectedScanId] = useState<string | null>(null);

  const activeClusterId =
    selectedClusterId || (clusters && clusters.length > 0 ? clusters[0]?.id ?? "" : "");

  const { data: posture, isLoading: postureLoading } =
    useSecurityPosture(activeClusterId);
  const { data: scans, isLoading: scansLoading } = useCVEScans(activeClusterId);
  const triggerScan = useTriggerScan();

  if (clustersLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!clusters || clusters.length === 0) {
    return (
      <div className="flex h-full items-center justify-center">
        <div className="text-center text-muted-foreground">
          <ShieldAlert className="mx-auto mb-2 h-12 w-12" />
          <p>No clusters configured. Add a cluster first.</p>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <ShieldAlert className="h-6 w-6" />
          <h1 className="text-2xl font-bold">Security</h1>
        </div>

        <button
          onClick={() => triggerScan.mutate(activeClusterId)}
          disabled={triggerScan.isPending}
          className="flex items-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          {triggerScan.isPending ? (
            <Loader2 className="h-4 w-4 animate-spin" />
          ) : (
            <RefreshCw className="h-4 w-4" />
          )}
          Scan Now
        </button>
      </div>

      {/* Cluster selector */}
      {clusters.length > 1 && (
        <div className="flex gap-2">
          {clusters.map((cluster) => (
            <button
              key={cluster.id}
              onClick={() => {
                setSelectedClusterId(cluster.id);
                setSelectedScanId(null);
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

      {/* Security Posture */}
      {postureLoading ? (
        <div className="h-32 animate-pulse rounded-lg border bg-card" />
      ) : posture ? (
        <SecurityPostureCard posture={posture} />
      ) : null}

      {/* Scan Schedule */}
      <ScanScheduleCard clusterId={activeClusterId} />

      {/* Vulnerability detail view or Scan history */}
      {selectedScanId ? (
        <VulnerabilityTable
          clusterId={activeClusterId}
          scanId={selectedScanId}
          onBack={() => setSelectedScanId(null)}
        />
      ) : (
        <div className="space-y-3">
          <h2 className="text-lg font-semibold">Scan History</h2>
          {scansLoading ? (
            <div className="h-48 animate-pulse rounded-lg border bg-card" />
          ) : (
            <ScanHistoryTable
              scans={scans ?? []}
              clusterId={activeClusterId}
              onSelectScan={setSelectedScanId}
            />
          )}
        </div>
      )}
    </div>
  );
}
