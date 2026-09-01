import { useState, useEffect } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Globe, AlertTriangle, ShieldAlert } from "lucide-react";
import { ApiClientError } from "@/lib/api-client";
import { privateAddressWarningFromError } from "@/lib/private-address";
import { usePermissions } from "@/hooks/usePermissions";
import {
  useVirtioWinMirror,
  useUpdateVirtioWinMirror,
} from "../api/virtio-win-queries";
import type { VirtioWinMirrorRequest } from "../types/virtio-win";

/**
 * Reads the 422 the API returns for a plain-http source. Same warn-then-confirm
 * shape as the private-address check, and deliberately a separate confirmation:
 * a private address exposes nothing, whereas http means the driver media a
 * Windows guest installs arrives unauthenticated — and upstream publishes no
 * checksum to fall back on.
 */
function insecureWarningFromError(err: unknown): string | null {
  if (
    !(err instanceof ApiClientError) ||
    err.status !== 422 ||
    err.body.error !== "insecure_source_confirm_required"
  ) {
    return null;
  }
  return err.body.message === ""
    ? "This source uses plain HTTP."
    : err.body.message;
}

/**
 * The instance-wide download source.
 *
 * Rendered on the cluster page rather than under Administration because this is
 * where its failure surfaces: an air-gapped install sees "Last check failed"
 * on the card above, and the fix belongs next to the symptom. It is labelled
 * as applying to every cluster, and gated on manage:settings rather than the
 * manage:storage that governs the rest of the card, because one write here
 * repoints every cluster's downloads at once.
 */
export function VirtioWinSourceCard() {
  const { data: mirror, isLoading } = useVirtioWinMirror();
  const updateMirror = useUpdateVirtioWinMirror();
  const { canManage } = usePermissions();
  const readOnly = !canManage("settings");

  const [baseUrl, setBaseUrl] = useState("");
  const [privateWarning, setPrivateWarning] = useState<string | null>(null);
  const [insecureWarning, setInsecureWarning] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (mirror) setBaseUrl(mirror.base_url);
  }, [mirror]);

  if (isLoading) {
    return <Skeleton className="h-64 w-full" />;
  }

  const upstream = mirror?.upstream_url ?? "";
  const dirty = baseUrl.trim() !== (mirror?.base_url ?? "");

  function save(confirmations: {
    allowPrivate?: boolean;
    allowInsecure?: boolean;
  }) {
    setSaved(false);
    // Only carried when the operator just clicked through the matching
    // warning — a confirmation must not persist across an edited URL.
    const body: VirtioWinMirrorRequest = { base_url: baseUrl.trim() };
    if (confirmations.allowPrivate === true) body.allow_private_address = true;
    if (confirmations.allowInsecure === true) body.allow_insecure = true;

    updateMirror.mutate(body, {
      onSuccess: () => {
        setPrivateWarning(null);
        setInsecureWarning(null);
        setSaved(true);
        setTimeout(() => {
          setSaved(false);
        }, 4000);
      },
      onError: (err: unknown) => {
        // Clear both first, so only the prompt that applies to THIS attempt is
        // on screen. An internal mirror on plain http trips the scheme check
        // and then the address check, and leaving the answered one up would
        // stack two warnings where one has already been dealt with.
        setInsecureWarning(null);
        setPrivateWarning(null);

        const insecure = insecureWarningFromError(err);
        if (insecure !== null) {
          setInsecureWarning(insecure);
          return;
        }
        const priv = privateAddressWarningFromError(err);
        if (priv !== null) {
          setPrivateWarning(
            `This source resolves to a private address (${priv.ip}). That is expected for an internal mirror.`,
          );
        }
        // Anything else is a real failure, already surfaced as a toast by the
        // mutation hook. Repeating it inline would say the same thing twice.
      },
    });
  }

  function handleChange(next: string) {
    setBaseUrl(next);
    // An edited URL invalidates whatever the last one was confirmed for.
    setPrivateWarning(null);
    setInsecureWarning(null);
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Globe className="h-5 w-5" />
          Download source
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-sm text-muted-foreground">
          Where releases are discovered and ISOs fetched from. Applies to{" "}
          <span className="font-medium text-foreground">every cluster</span> on
          this install. Leave it empty to follow upstream; point it at a mirror
          of the upstream tree for an air-gapped network. Both Nexara and the
          Proxmox nodes have to reach it.
        </p>

        <div className="space-y-2">
          <Label htmlFor="virtio-win-source">Base URL</Label>
          <Input
            id="virtio-win-source"
            value={baseUrl}
            disabled={readOnly}
            placeholder={upstream}
            onChange={(e) => {
              handleChange(e.target.value);
            }}
          />
          <p className="text-xs text-muted-foreground">
            {mirror?.base_url === ""
              ? `Following upstream: ${upstream}`
              : `Currently: ${mirror?.effective_url ?? ""}`}
          </p>
        </div>

        <div className="rounded-md border bg-muted/40 p-3 text-xs text-muted-foreground">
          <p className="font-medium text-foreground">
            What the mirror has to look like
          </p>
          <p className="mt-1">
            A copy of the upstream tree, so that{" "}
            <code className="rounded bg-muted px-1">
              &lt;base&gt;/archive-virtio/virtio-win-&lt;version&gt;/
            </code>{" "}
            holds each ISO. That is what
            <code className="mx-1 rounded bg-muted px-1">wget -m -np</code>
            of the upstream directory produces. The
            <code className="mx-1 rounded bg-muted px-1">stable-virtio/</code>
            redirect is not needed &mdash; without it, the newest version in the
            archive index is used instead.
          </p>
        </div>

        {insecureWarning !== null && (
          <div className="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-3 dark:border-amber-700 dark:bg-amber-950">
            <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
            <div className="space-y-2 text-xs text-amber-700 dark:text-amber-300">
              <p>{insecureWarning}</p>
              <Button
                size="sm"
                variant="outline"
                disabled={updateMirror.isPending}
                onClick={() => {
                  save({ allowInsecure: true });
                }}
              >
                Use it anyway
              </Button>
            </div>
          </div>
        )}

        {privateWarning !== null && (
          <div className="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-3 dark:border-amber-700 dark:bg-amber-950">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
            <div className="space-y-2 text-xs text-amber-700 dark:text-amber-300">
              <p>{privateWarning}</p>
              <Button
                size="sm"
                variant="outline"
                disabled={updateMirror.isPending}
                onClick={() => {
                  // Both confirmations ride along: an internal mirror on plain
                  // http trips the scheme check first and the address check
                  // second, and making the operator click twice for one URL
                  // they already vouched for is noise.
                  save({ allowPrivate: true, allowInsecure: true });
                }}
              >
                Use it anyway
              </Button>
            </div>
          </div>
        )}

        <div className="flex items-center gap-3">
          <Button
            onClick={() => {
              save({});
            }}
            disabled={readOnly || updateMirror.isPending || !dirty}
          >
            {updateMirror.isPending ? "Saving..." : "Save source"}
          </Button>
          {baseUrl.trim() !== "" && !readOnly && (
            <Button
              variant="ghost"
              disabled={updateMirror.isPending}
              onClick={() => {
                handleChange("");
              }}
            >
              Reset to upstream
            </Button>
          )}
          {saved && (
            <span className="text-sm text-muted-foreground">
              Saved. Run a check to rebuild the release list from this source.
            </span>
          )}
        </div>

        {readOnly && (
          <p className="text-xs text-muted-foreground">
            Changing the source needs the Manage settings permission.
          </p>
        )}
      </CardContent>
    </Card>
  );
}
