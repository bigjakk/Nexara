import { useState, useEffect } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Globe,
  AlertTriangle,
  ShieldAlert,
  type LucideIcon,
} from "lucide-react";
import { WarningCallout } from "@/components/WarningCallout";
import {
  confirmRequiredFromError,
  confirmDetailString,
  type ConfirmRequired,
} from "@/lib/confirm-gate";
import { usePermissions } from "@/hooks/usePermissions";
import {
  useVirtioWinMirror,
  useUpdateVirtioWinMirror,
} from "../api/virtio-win-queries";
import { MIRROR_CONFIRM_CODES } from "../types/virtio-win";
import type { VirtioWinMirrorRequest } from "../types/virtio-win";

type Acknowledgement = Omit<VirtioWinMirrorRequest, "base_url">;
type MirrorCode = (typeof MIRROR_CONFIRM_CODES)[number];

interface MirrorGate {
  icon: LucideIcon;
  /** What confirming this gate — and only this gate — waives. */
  confirm: Acknowledgement;
  text: (warning: ConfirmRequired) => string;
}

/**
 * What each gate is refusing, and what confirming it would acknowledge.
 *
 * A Record over MIRROR_CONFIRM_CODES rather than a chain of ifs, so adding a
 * code there without deciding what it waives is a compile error. Each
 * acknowledgement waives a different check, and one gate silently inheriting
 * another's would submit a confirmation the operator was never shown.
 */
const MIRROR_GATES: Record<MirrorCode, MirrorGate> = {
  insecure_source_confirm_required: {
    icon: ShieldAlert,
    confirm: { allow_insecure: true },
    text: (warning) =>
      warning.message === "" ? "This source uses plain HTTP." : warning.message,
  },
  private_address_confirm_required: {
    icon: AlertTriangle,
    // The http acknowledgement rides along: an internal mirror on plain http
    // trips the scheme check first and the address check second, and making
    // the operator click twice for one URL they have already vouched for is
    // noise.
    confirm: { allow_private_address: true, allow_insecure: true },
    text: (warning) => {
      const ip = confirmDetailString(warning, "ip");
      const where = ip === null || ip === "" ? "" : ` (${ip})`;
      return `This source resolves to a private address${where}. That is expected for an internal mirror.`;
    },
  },
};

/** `ConfirmRequired.code` is a plain string; this is the safe way in. */
function mirrorGate(code: string): MirrorGate | null {
  return Object.hasOwn(MIRROR_GATES, code)
    ? MIRROR_GATES[code as MirrorCode]
    : null;
}

interface MirrorWarningProps {
  warning: ConfirmRequired;
  pending: boolean;
  onConfirm: (confirmations: Acknowledgement) => void;
}

/**
 * The one gate the API is holding this save behind. Deliberately two separate
 * confirmations rather than one: a private address exposes nothing, whereas
 * http means the driver media a Windows guest installs arrives unauthenticated
 * — and upstream publishes no checksum to fall back on.
 */
function MirrorWarning({ warning, pending, onConfirm }: MirrorWarningProps) {
  const gate = mirrorGate(warning.code);
  if (gate === null) return null;
  return (
    <WarningCallout icon={gate.icon}>
      <p>{gate.text(warning)}</p>
      <div className="pt-1">
        <Button
          size="sm"
          variant="outline"
          disabled={pending}
          onClick={() => {
            onConfirm(gate.confirm);
          }}
        >
          Use it anyway
        </Button>
      </div>
    </WarningCallout>
  );
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
  // One at a time by construction: the API answers with at most one gate per
  // attempt, so a second warning can only ever be a stale one left on screen.
  const [warning, setWarning] = useState<ConfirmRequired | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (mirror) setBaseUrl(mirror.base_url);
  }, [mirror]);

  if (isLoading) {
    return <Skeleton className="h-64 w-full" />;
  }

  const upstream = mirror?.upstream_url ?? "";
  const dirty = baseUrl.trim() !== (mirror?.base_url ?? "");

  // Confirmations are the request's own fields rather than a camelCase echo of
  // them, and are only ever passed by the button under the matching warning —
  // an acknowledgement must not persist across an edited URL.
  function save(confirmations: Acknowledgement = {}) {
    setSaved(false);
    updateMirror.mutate(
      { base_url: baseUrl.trim(), ...confirmations },
      {
        onSuccess: () => {
          setWarning(null);
          setSaved(true);
          setTimeout(() => {
            setSaved(false);
          }, 4000);
        },
        // A gate is a prompt, not a failure. Anything else is a real error,
        // already surfaced as a toast by the mutation hook — repeating it
        // inline would say the same thing twice — and it clears the prompt so
        // only what applies to THIS attempt is on screen.
        onError: (err: unknown) => {
          setWarning(confirmRequiredFromError(err, MIRROR_CONFIRM_CODES, null));
        },
      },
    );
  }

  function handleChange(next: string) {
    setBaseUrl(next);
    // An edited URL invalidates whatever the last one was confirmed for.
    setWarning(null);
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

        {warning !== null && (
          <MirrorWarning
            warning={warning}
            pending={updateMirror.isPending}
            onConfirm={save}
          />
        )}

        <div className="flex items-center gap-3">
          <Button
            onClick={() => {
              save();
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
