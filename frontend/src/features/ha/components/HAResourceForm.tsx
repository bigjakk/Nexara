import { useEffect, useMemo, useState } from "react";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  useCreateHAResource,
  useUpdateHAResource,
  useHAGroups,
  HA_RESOURCE_DEFAULTS,
  haResourceHasFailback,
  parseRetryCount,
  type HAResource,
  type UpdateHAResourceRequest,
} from "@/features/ha/api/ha-queries";
import { ApiClientError } from "@/lib/api-client";
import type { VMResponse } from "@/types/api";

function describeHAResourceError(err: unknown): string {
  if (err instanceof ApiClientError) return err.body.message;
  if (err instanceof Error) return err.message;
  return String(err);
}

const HA_STATES = ["started", "stopped", "disabled", "ignored"] as const;

function vmToSID(vm: VMResponse): string {
  return `${vm.type === "lxc" ? "ct" : "vm"}:${String(vm.vmid)}`;
}

interface CommonProps {
  clusterId: string;
  /**
   * The cluster's Proxmox VE version. It decides whether failback is offered
   * and sent at all: a PVE 8 resource has none (haResourceHasFailback).
   */
  pveVersion: string;
  onSuccess: () => void;
}

interface CreateProps extends CommonProps {
  mode: "create";
  availableVMs: VMResponse[];
}

interface EditProps extends CommonProps {
  mode: "edit";
  resource: HAResource;
}

type Props = CreateProps | EditProps;

/** The fields as the form shows them. */
interface HAResourceFields {
  state: string;
  /** "" for no group. */
  group: string;
  maxRestart: string;
  maxRelocate: string;
  failback: boolean;
  comment: string;
}

/**
 * What the form shows for a resource: each value it stores, or Proxmox's
 * default where it stores none (HA_RESOURCE_DEFAULTS). With no resource — the
 * create form — every field starts at the default.
 */
function fieldsFor(resource: HAResource | null): HAResourceFields {
  return {
    state: resource?.state || HA_RESOURCE_DEFAULTS.state,
    group: resource?.group ?? "",
    maxRestart: String(
      resource?.max_restart ?? HA_RESOURCE_DEFAULTS.max_restart,
    ),
    maxRelocate: String(
      resource?.max_relocate ?? HA_RESOURCE_DEFAULTS.max_relocate,
    ),
    failback: (resource?.failback ?? HA_RESOURCE_DEFAULTS.failback) === 1,
    comment: resource?.comment ?? "",
  };
}

/**
 * The input ceiling for a retry count: 10, as Proxmox's own editor offers
 * (ResourceEdit.js maxValue), or the resource's stored count when that is
 * higher. Proxmox has no maximum (Resources.pm declares only `minimum => 0`),
 * so ha-manager or pvesh can store more than 10, and a fixed max="10" made the
 * browser's constraint validation refuse EVERY save of such a resource — an
 * edit of the comment included — over a value the operator never touched.
 */
function countCeiling(stored: number | undefined): number {
  return Math.max(10, stored ?? 0);
}

/**
 * The body of an edit: ONLY the fields the operator changed from what the form
 * showed when it opened, compared as the values they would send — so "03"
 * typed over a shown 3 is no change. That is the operator's decision, and the
 * point of it: an untouched field is never written, so a value the read side
 * got wrong cannot write itself back on a save that never touched it, and
 * neither can a property this form does not model at all (PVE 9's
 * auto-rebalance). Proxmox's own editor does otherwise: it submits every field,
 * since an InputPanel with an onGetValues drops dirtyOnly (proxmox-widget-toolkit
 * src/panel/InputPanel.js, getValues), and ResourceEdit.js's onGetValues
 * (pve-manager www/manager6/ha/) turns one at its default into delete=<field>.
 *
 * A count that does not parse (parseRetryCount) is left out, as the form
 * always did. `ceilings` are the inputs' own maxima (countCeiling).
 */
function changedFields(
  shown: HAResourceFields,
  current: HAResourceFields,
  ceilings: { restart: number; relocate: number },
): UpdateHAResourceRequest {
  const body: UpdateHAResourceRequest = {};
  if (current.state !== shown.state) body.state = current.state;
  if (current.group !== shown.group) body.group = current.group;
  const restart = parseRetryCount(current.maxRestart, ceilings.restart);
  if (
    restart !== undefined &&
    restart !== parseRetryCount(shown.maxRestart, ceilings.restart)
  ) {
    body.max_restart = restart;
  }
  const relocate = parseRetryCount(current.maxRelocate, ceilings.relocate);
  if (
    relocate !== undefined &&
    relocate !== parseRetryCount(shown.maxRelocate, ceilings.relocate)
  ) {
    body.max_relocate = relocate;
  }
  if (current.failback !== shown.failback) {
    body.failback = current.failback ? 1 : 0;
  }
  if (current.comment !== shown.comment) body.comment = current.comment;
  return body;
}

export function HAResourceForm(props: Props) {
  const groupsQuery = useHAGroups(props.clusterId);
  const createMut = useCreateHAResource(props.clusterId);
  const updateMut = useUpdateHAResource(props.clusterId);

  const resource = props.mode === "edit" ? props.resource : null;
  // What the form showed on opening, which an edit's changes are measured
  // against. Memoised on the resource so it moves only when the resource does.
  const shown = useMemo(() => fieldsFor(resource), [resource]);
  // On PVE 8 the switch is not rendered, so an edit can never change failback
  // and changedFields never sends it; the create below leaves it out too.
  const hasFailback = haResourceHasFailback(props.pveVersion);

  const [sid, setSID] = useState(resource?.sid ?? "");
  const [state, setState] = useState(shown.state);
  const [group, setGroup] = useState(shown.group);
  const [maxRestart, setMaxRestart] = useState<string>(shown.maxRestart);
  const [maxRelocate, setMaxRelocate] = useState<string>(shown.maxRelocate);
  const [failback, setFailback] = useState<boolean>(shown.failback);
  const [comment, setComment] = useState<string>(shown.comment);

  // Re-seeds the fields when a DIFFERENT resource is handed in, so the fields
  // and `shown` always describe the same resource. ClusterHATab never does
  // that today — its dialog unmounts the form between resources — so this is
  // for a caller that swaps the resource in place, where without it the diff
  // would be measured against one resource's baseline with another's fields.
  // Keyed on the resource rather than on props: props is a new object on
  // every render of the parent, and the HA tab re-renders whenever one of its
  // polled queries returns new data (HA status and manager status every 30
  // seconds, the guest list every 60) — so keyed on props, this reset the
  // operator's unsaved changes under them, and a change that is reset is one
  // changedFields never sees.
  useEffect(() => {
    if (!resource) return;
    setSID(resource.sid);
    setState(shown.state);
    setGroup(shown.group);
    setMaxRestart(shown.maxRestart);
    setMaxRelocate(shown.maxRelocate);
    setFailback(shown.failback);
    setComment(shown.comment);
  }, [resource, shown]);

  // The count inputs' maxima: 10, or a stored count above it (countCeiling).
  const ceilings = {
    restart: countCeiling(resource?.max_restart),
    relocate: countCeiling(resource?.max_relocate),
  };

  const handleSubmit = (e: React.SyntheticEvent) => {
    e.preventDefault();
    const maxRestartNum = parseRetryCount(maxRestart, ceilings.restart);
    const maxRelocateNum = parseRetryCount(maxRelocate, ceilings.relocate);
    const groupValue = group === "__none__" ? "" : group;

    if (props.mode === "create") {
      createMut.mutate(
        {
          sid,
          state,
          ...(groupValue ? { group: groupValue } : {}),
          ...(maxRestartNum !== undefined
            ? { max_restart: maxRestartNum }
            : {}),
          ...(maxRelocateNum !== undefined
            ? { max_relocate: maxRelocateNum }
            : {}),
          ...(hasFailback ? { failback: failback ? 1 : 0 } : {}),
          ...(comment ? { comment } : {}),
        },
        { onSuccess: props.onSuccess },
      );
    } else {
      const changes = changedFields(
        shown,
        {
          state,
          group: groupValue,
          maxRestart,
          maxRelocate,
          failback,
          comment,
        },
        ceilings,
      );
      // Nothing changed is nothing to write — not an empty PUT that Proxmox
      // would accept and Nexara would audit as an update.
      if (Object.keys(changes).length === 0) {
        props.onSuccess();
        return;
      }
      updateMut.mutate(
        { sid: props.resource.sid, ...changes },
        { onSuccess: props.onSuccess },
      );
    }
  };

  // In the edit form, a setting the resource leaves to Proxmox says so.
  const unsetHint = (unset: boolean, defaultText: string) =>
    resource && unset ? (
      <p className="text-xs text-muted-foreground">
        Not set on this resource, so Proxmox&apos;s default applies:{" "}
        {defaultText}.
      </p>
    ) : null;

  const isPending =
    props.mode === "create" ? createMut.isPending : updateMut.isPending;
  const mutError = props.mode === "create" ? createMut.error : updateMut.error;
  const groups = groupsQuery.data ?? [];

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      {props.mode === "create" ? (
        <div className="space-y-2">
          <Label>VM / Container</Label>
          <Select value={sid} onValueChange={setSID}>
            <SelectTrigger>
              <SelectValue placeholder="Select a VM or container" />
            </SelectTrigger>
            <SelectContent>
              {props.availableVMs.map((vm) => (
                <SelectItem key={vm.id} value={vmToSID(vm)}>
                  {vmToSID(vm)} — {vm.name}
                </SelectItem>
              ))}
              {props.availableVMs.length === 0 && (
                <div className="px-2 py-1.5 text-sm text-muted-foreground">
                  All VMs/CTs are already HA resources
                </div>
              )}
            </SelectContent>
          </Select>
        </div>
      ) : (
        <div className="space-y-2">
          <Label>Resource</Label>
          <Input value={sid} disabled className="font-mono" />
        </div>
      )}

      <div className="space-y-2">
        <Label>Requested State</Label>
        <Select value={state} onValueChange={setState}>
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {HA_STATES.map((s) => (
              <SelectItem key={s} value={s}>
                {s}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="space-y-2">
        <Label>Group</Label>
        <Select
          value={group === "" ? "__none__" : group}
          onValueChange={setGroup}
        >
          <SelectTrigger>
            <SelectValue placeholder="— None —" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="__none__">— None —</SelectItem>
            {groups.map((g) => (
              <SelectItem key={g.group} value={g.group}>
                {g.group}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-2">
          <Label htmlFor="max-restart">Max Restart</Label>
          <Input
            id="max-restart"
            type="number"
            min="0"
            max={String(ceilings.restart)}
            value={maxRestart}
            onChange={(e) => {
              setMaxRestart(e.target.value);
            }}
          />
          {unsetHint(
            resource?.max_restart === undefined,
            String(HA_RESOURCE_DEFAULTS.max_restart),
          )}
        </div>
        <div className="space-y-2">
          <Label htmlFor="max-relocate">Max Relocate</Label>
          <Input
            id="max-relocate"
            type="number"
            min="0"
            max={String(ceilings.relocate)}
            value={maxRelocate}
            onChange={(e) => {
              setMaxRelocate(e.target.value);
            }}
          />
          {unsetHint(
            resource?.max_relocate === undefined,
            String(HA_RESOURCE_DEFAULTS.max_relocate),
          )}
        </div>
      </div>

      {hasFailback && (
        <div className="flex items-center justify-between rounded-md border p-3">
          <div>
            <Label htmlFor="failback" className="cursor-pointer">
              Failback
            </Label>
            <p className="text-xs text-muted-foreground">
              Move back to higher-priority node when available.
            </p>
            {unsetHint(
              resource?.failback === undefined,
              HA_RESOURCE_DEFAULTS.failback === 1 ? "on" : "off",
            )}
          </div>
          <Switch
            id="failback"
            checked={failback}
            onCheckedChange={setFailback}
          />
        </div>
      )}

      <div className="space-y-2">
        <Label htmlFor="comment">Comment</Label>
        <Textarea
          id="comment"
          value={comment}
          onChange={(e) => {
            setComment(e.target.value);
          }}
          rows={2}
        />
      </div>

      {mutError && (
        <div className="flex items-start gap-2 rounded-md border border-destructive bg-destructive/10 p-3 text-sm text-destructive">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <span>{describeHAResourceError(mutError)}</span>
        </div>
      )}

      <Button
        type="submit"
        disabled={isPending || (props.mode === "create" && !sid)}
      >
        {isPending ? "Saving..." : props.mode === "create" ? "Create" : "Save"}
      </Button>
    </form>
  );
}
