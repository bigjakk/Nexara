import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useTranslation } from "react-i18next";
import type { AptPackage } from "@/types/api";

interface PackagePreviewTableProps {
  packages: AptPackage[];
  /** Packages the job holds back (its package_excludes), marked as such. */
  heldBack?: readonly string[] | undefined;
}

export function PackagePreviewTable({
  packages,
  heldBack = [],
}: PackagePreviewTableProps) {
  const { t } = useTranslation("security");
  if (packages.length === 0) {
    return (
      <p className="py-4 text-center text-sm text-muted-foreground">
        No pending updates
      </p>
    );
  }

  return (
    <div className="max-h-64 overflow-auto">
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead>Package</TableHead>
            <TableHead>Current</TableHead>
            <TableHead>Available</TableHead>
            <TableHead>Priority</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {packages.map((pkg) => (
            <TableRow key={`${pkg.Package}-${pkg.Version}`}>
              <TableCell className="py-1.5 font-mono text-xs">
                {pkg.Package}
                {heldBack.includes(pkg.Package) && (
                  <span className="ml-2 rounded-[5px] bg-foreground/[0.07] px-1.5 py-0.5 font-sans text-[11.5px] text-muted-foreground">
                    {t("packageHeldBack")}
                  </span>
                )}
              </TableCell>
              <TableCell className="py-1.5 text-xs text-muted-foreground">
                {pkg.OldVersion}
              </TableCell>
              <TableCell className="py-1.5 text-xs">{pkg.Version}</TableCell>
              <TableCell className="py-1.5 text-xs text-muted-foreground">
                {pkg.Priority}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
