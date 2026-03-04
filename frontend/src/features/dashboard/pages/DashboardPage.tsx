import { useDashboardData } from "../api/dashboard-queries";
import { StatsOverview } from "../components/StatsOverview";
import { ClusterCard } from "../components/ClusterCard";
import { EmptyState } from "../components/EmptyState";

export function DashboardPage() {
  const { data, isLoading, error } = useDashboardData();

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Dashboard</h1>

      {error != null ? (
        <div className="rounded-lg border border-destructive bg-destructive/10 p-4 text-destructive">
          Failed to load dashboard data. Please try again.
        </div>
      ) : (
        <>
          <StatsOverview
            totalNodes={data?.totalNodes ?? 0}
            totalVMs={data?.totalVMs ?? 0}
            totalContainers={data?.totalContainers ?? 0}
            totalStorageBytes={data?.totalStorageBytes ?? 0}
            isLoading={isLoading}
          />

          {!isLoading && data?.clusters.length === 0 && <EmptyState />}

          {data != null && data.clusters.length > 0 && (
            <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
              {data.clusters.map((summary) => (
                <ClusterCard key={summary.cluster.id} summary={summary} />
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}
