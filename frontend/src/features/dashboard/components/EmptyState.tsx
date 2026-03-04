import { ServerCrash } from "lucide-react";

export function EmptyState() {
  return (
    <div className="flex flex-col items-center justify-center rounded-lg border border-dashed p-12 text-center">
      <ServerCrash className="h-12 w-12 text-muted-foreground" />
      <h3 className="mt-4 text-lg font-semibold">No clusters registered</h3>
      <p className="mt-2 text-sm text-muted-foreground">
        Add a Proxmox cluster to get started with monitoring your
        infrastructure.
      </p>
    </div>
  );
}
