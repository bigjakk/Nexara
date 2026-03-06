import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { Skeleton } from "@/components/ui/skeleton";

interface ClusterSelectorProps {
  value: string;
  onChange: (clusterId: string) => void;
}

export function ClusterSelector({ value, onChange }: ClusterSelectorProps) {
  const { data: clusters, isLoading } = useClusters();

  if (isLoading) {
    return <Skeleton className="h-9 w-64" />;
  }

  if (!clusters || clusters.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">No clusters available</p>
    );
  }

  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger className="w-64">
        <SelectValue placeholder="Select a cluster" />
      </SelectTrigger>
      <SelectContent>
        {clusters.map((cluster) => (
          <SelectItem key={cluster.id} value={cluster.id}>
            {cluster.name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
