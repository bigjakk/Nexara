import { ArrowLeftRight } from "lucide-react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";
import { useMigrationJobs } from "../api/migration-queries";
import { MigrateWizard, StatusBadge } from "../components/MigrateWizard";

export function MigrationsPage() {
  const { data: jobs, isLoading } = useMigrationJobs();

  return (
    <div className="space-y-6 p-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <ArrowLeftRight className="h-6 w-6 text-primary" />
          <h1 className="text-2xl font-semibold">Migrations</h1>
        </div>
        <MigrateWizard />
      </div>

      {isLoading ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} className="h-12 w-full" />
          ))}
        </div>
      ) : !jobs || jobs.length === 0 ? (
        <p className="text-muted-foreground">
          No migration jobs yet. Click &quot;New Migration&quot; to create one.
        </p>
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>VMID</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Migration</TableHead>
                <TableHead>Source Node</TableHead>
                <TableHead>Target Node</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Progress</TableHead>
                <TableHead>Created</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {jobs.map((job) => (
                <TableRow key={job.id}>
                  <TableCell className="font-medium">{job.vmid}</TableCell>
                  <TableCell>{job.vm_type}</TableCell>
                  <TableCell>{job.migration_type}</TableCell>
                  <TableCell>{job.source_node}</TableCell>
                  <TableCell>{job.target_node || "—"}</TableCell>
                  <TableCell>
                    <StatusBadge status={job.status} />
                  </TableCell>
                  <TableCell>
                    {job.status === "migrating" ? (
                      <div className="flex items-center gap-2">
                        <div className="h-2 w-20 overflow-hidden rounded-full bg-muted">
                          <div
                            className="h-full bg-primary transition-all"
                            style={{
                              width: `${String(Math.max(job.progress * 100, 5))}%`,
                            }}
                          />
                        </div>
                        <span className="text-xs text-muted-foreground">
                          {String(Math.round(job.progress * 100))}%
                        </span>
                      </div>
                    ) : job.status === "completed" ? (
                      "100%"
                    ) : (
                      "—"
                    )}
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {new Date(job.created_at).toLocaleString()}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
