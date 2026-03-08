import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { AptPackage } from "@/types/api";

interface PackagePreviewTableProps {
  packages: AptPackage[];
}

export function PackagePreviewTable({ packages }: PackagePreviewTableProps) {
  if (packages.length === 0) {
    return (
      <p className="py-4 text-center text-sm text-muted-foreground">
        No pending updates
      </p>
    );
  }

  return (
    <div className="max-h-64 overflow-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Package</TableHead>
            <TableHead>Current</TableHead>
            <TableHead>Available</TableHead>
            <TableHead>Priority</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {packages.map((pkg) => (
            <TableRow key={`${pkg.Package}-${pkg.Version}`}>
              <TableCell className="font-mono text-xs">{pkg.Package}</TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {pkg.OldVersion}
              </TableCell>
              <TableCell className="text-xs">{pkg.Version}</TableCell>
              <TableCell className="text-xs">{pkg.Priority}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
