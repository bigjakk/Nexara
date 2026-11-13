import { ExternalLink, Loader2 } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useScanVulnerabilities } from "../api/cve-queries";
import { SeverityBadge } from "./SeverityBadge";

/**
 * Dialog that lists every KEV-flagged (actively-exploited) vulnerability for
 * a given scan. Opened by the Review button on the PosturePanel's "X actively
 * exploited" line.
 *
 * Each CVE-ID is a deep link to NIST NVD for the canonical detail page; the
 * KEV flame badge links to CISA's KEV catalog filtered to that CVE so users
 * can see CISA's required-action / due-date / ransomware notes.
 */

interface KEVDetailDialogProps {
  clusterId: string;
  scanId: string | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

function nvdURL(cveID: string): string {
  return `https://nvd.nist.gov/vuln/detail/${encodeURIComponent(cveID)}`;
}

function kevURL(cveID: string): string {
  return `https://www.cisa.gov/known-exploited-vulnerabilities-catalog?search_api_fulltext=${encodeURIComponent(cveID)}`;
}

export function KEVDetailDialog({
  clusterId,
  scanId,
  open,
  onOpenChange,
}: KEVDetailDialogProps) {
  const { data: vulns, isLoading } = useScanVulnerabilities(
    clusterId,
    scanId ?? "",
    scanId ? { kev: true } : undefined,
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] max-w-5xl overflow-hidden sm:rounded-lg">
        <DialogHeader>
          <DialogTitle>Actively exploited vulnerabilities</DialogTitle>
          <p className="text-sm text-muted-foreground">
            Listed in CISA&apos;s{" "}
            <a
              href="https://www.cisa.gov/known-exploited-vulnerabilities-catalog"
              target="_blank"
              rel="noopener noreferrer"
              className="underline hover:text-foreground"
            >
              Known Exploited Vulnerabilities catalog
            </a>
            . Patch these first. A CVE-ID opens its full record on NIST NVD; the
            KEV chip beside it opens CISA&apos;s entry, with the required action
            and due date.
          </p>
        </DialogHeader>

        <div className="-mx-6 max-h-[60vh] overflow-y-auto px-6">
          {isLoading ? (
            <div className="flex items-center justify-center py-12">
              <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          ) : !vulns || vulns.length === 0 ? (
            <div className="py-12 text-center text-sm text-muted-foreground">
              No KEV-flagged vulnerabilities in this scan.
            </div>
          ) : (
            <table className="w-full text-sm">
              <thead className="sticky top-0 bg-background">
                <tr className="border-b text-left text-xs font-medium text-muted-foreground">
                  <th className="px-2 py-2 font-medium">CVE</th>
                  <th className="px-2 py-2 font-medium">Risk</th>
                  <th className="px-2 py-2 font-medium">Score</th>
                  <th className="px-2 py-2 font-medium">Package</th>
                  <th className="px-2 py-2 font-medium">Installed → fixed</th>
                </tr>
              </thead>
              <tbody>
                {vulns.map((v) => (
                  <tr key={v.id} className="border-b">
                    <td className="px-2 py-2 font-mono text-xs">
                      <div className="flex items-center gap-1.5">
                        <a
                          href={nvdURL(v.cve_id)}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex items-center gap-1 text-foreground underline-offset-2 hover:underline"
                        >
                          {v.cve_id}
                          <ExternalLink className="h-3 w-3 opacity-60" />
                        </a>
                        <a
                          href={kevURL(v.cve_id)}
                          target="_blank"
                          rel="noopener noreferrer"
                          title="View CISA KEV entry"
                          className="inline-flex items-center rounded-md bg-red-500/15 px-1.5 py-0.5 text-[11px] font-semibold text-red-700 hover:bg-red-500/25 dark:text-red-400"
                        >
                          KEV
                        </a>
                      </div>
                    </td>
                    <td className="px-2 py-2">
                      <SeverityBadge severity={v.risk_severity} />
                    </td>
                    <td className="px-2 py-2 tabular-nums">
                      {v.risk_score > 0 ? v.risk_score.toFixed(1) : "-"}
                    </td>
                    <td className="px-2 py-2 font-medium">{v.package_name}</td>
                    <td className="px-2 py-2 font-mono text-xs text-muted-foreground">
                      {v.current_version || "—"}
                      {v.fixed_version ? ` → ${v.fixed_version}` : ""}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
