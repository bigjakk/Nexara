import { useState } from "react";
import { ShieldAlert } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { EditClusterDialog } from "./EditClusterDialog";
import { useVerifyClusterCertificate } from "../api/cluster-queries";
import type { ClusterResponse } from "@/types/api";

/**
 * The health-issue type raised by the server when the certificate the
 * cluster's configured endpoint presents no longer matches the pinned
 * fingerprint. Kept in one place so the banner and any future consumer agree.
 */
export const TLS_FINGERPRINT_ISSUE = "tls_fingerprint_changed";

interface ClusterCertificateBannerProps {
  cluster: ClusterResponse;
}

/**
 * Surfaces a changed Proxmox certificate on the cluster it affects.
 *
 * This needs its own banner rather than sitting in the generic issue list
 * because of how the failure presents: every live Proxmox call for the cluster
 * fails the TLS handshake, but the collector fails over to another member and
 * keeps inventory and metrics flowing. So the cluster looks healthy, and the
 * only symptom is tabs that render empty or 502. Without something saying why,
 * the cause is invisible.
 *
 * "Verify certificate" asks the server to corroborate the endpoint's current
 * certificate two independent ways — a live TLS handshake, and what the
 * cluster itself reports for that node, which the collector read over a
 * connection pinned to a fingerprint already trusted — and re-pin only if they
 * agree. That corroboration is what makes a single click acceptable; nothing
 * here trusts a certificate on its own say-so, and the server refuses rather
 * than resolving a disagreement.
 *
 * "Edit manually" stays for the cases the server will not decide: an endpoint
 * with no observed certificate to check against, or a genuine disagreement the
 * operator has to investigate.
 */
export function ClusterCertificateBanner({
  cluster,
}: ClusterCertificateBannerProps) {
  const [editOpen, setEditOpen] = useState(false);
  const verify = useVerifyClusterCertificate(cluster.id);

  const issue = (cluster.issues ?? []).find(
    (i) => i.type === TLS_FINGERPRINT_ISSUE,
  );
  if (!issue) return null;

  function handleVerify() {
    verify.mutate(undefined, {
      onSuccess: (result) => {
        toast.success(result.message);
      },
      onError: (error) => {
        // The server refuses on a disagreement rather than resolving it, so
        // surface its reason and leave the manual route open.
        toast.error(
          error instanceof Error ? error.message : "Verification failed",
        );
      },
    });
  }

  return (
    <>
      <div className="mb-4 rounded-md border border-destructive/30 bg-destructive/10 p-4">
        <div className="flex items-start gap-3">
          <ShieldAlert className="mt-0.5 h-5 w-5 shrink-0 text-destructive" />
          <div className="min-w-0 flex-1">
            <p className="font-medium text-destructive">{issue.summary}</p>
            {/* break-all: the detail ends in a fingerprint, which has no
                spaces to wrap on and would otherwise widen the page. */}
            <p className="mt-1 text-sm break-all text-destructive/90">
              {issue.detail}
            </p>
            <p className="mt-2 text-xs text-muted-foreground">
              Inventory and metrics keep updating through the cluster&apos;s
              other members, so this will not show up as a sync failure.
            </p>
          </div>
          <div className="flex shrink-0 flex-col gap-2">
            <Button
              size="sm"
              onClick={handleVerify}
              disabled={verify.isPending}
            >
              {verify.isPending ? "Verifying…" : "Verify certificate"}
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                setEditOpen(true);
              }}
            >
              Edit manually
            </Button>
          </div>
        </div>
      </div>

      {/* Mounted only while open, matching the other two call sites.
          EditClusterDialog seeds its form from props once and never re-syncs,
          so a permanently mounted copy would keep the api_url and token id it
          saw on first render — submitting could then quietly revert an edit
          made elsewhere, and a typed token secret would survive Cancel. */}
      {editOpen && (
        <EditClusterDialog
          cluster={cluster}
          open={editOpen}
          onOpenChange={setEditOpen}
        />
      )}
    </>
  );
}
