import { useEffect, useState } from "react";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import {
  useCreateHAResource,
  useUpdateHAResource,
  useHAGroups,
  type HAResource,
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

export function HAResourceForm(props: Props) {
  const groupsQuery = useHAGroups(props.clusterId);
  const createMut = useCreateHAResource(props.clusterId);
  const updateMut = useUpdateHAResource(props.clusterId);

  const initial = props.mode === "edit" ? props.resource : null;

  const [sid, setSID] = useState(initial?.sid ?? "");
  const [state, setState] = useState(initial?.state ?? "started");
  const [group, setGroup] = useState(initial?.group ?? "");
  const [maxRestart, setMaxRestart] = useState<string>(
    initial?.max_restart != null ? String(initial.max_restart) : "1",
  );
  const [maxRelocate, setMaxRelocate] = useState<string>(
    initial?.max_relocate != null ? String(initial.max_relocate) : "1",
  );
  const [failback, setFailback] = useState<boolean>(
    initial?.failback == null ? true : initial.failback === 1,
  );
  const [comment, setComment] = useState<string>(initial?.comment ?? "");

  useEffect(() => {
    if (props.mode === "edit") {
      setSID(props.resource.sid);
      setState(props.resource.state);
      setGroup(props.resource.group);
      setMaxRestart(props.resource.max_restart != null ? String(props.resource.max_restart) : "1");
      setMaxRelocate(String(props.resource.max_relocate));
      setFailback(props.resource.failback == null ? true : props.resource.failback === 1);
      setComment(props.resource.comment ?? "");
    }
  }, [props]);

  const handleSubmit = (e: React.SyntheticEvent) => {
    e.preventDefault();
    const maxRestartNum = Number.parseInt(maxRestart, 10);
    const maxRelocateNum = Number.parseInt(maxRelocate, 10);
    const groupValue = group === "__none__" ? "" : group;

    if (props.mode === "create") {
      createMut.mutate(
        {
          sid,
          state,
          ...(groupValue ? { group: groupValue } : {}),
          ...(Number.isFinite(maxRestartNum) ? { max_restart: maxRestartNum } : {}),
          ...(Number.isFinite(maxRelocateNum) ? { max_relocate: maxRelocateNum } : {}),
          failback: failback ? 1 : 0,
          ...(comment ? { comment } : {}),
        },
        { onSuccess: props.onSuccess },
      );
    } else {
      updateMut.mutate(
        {
          sid: props.resource.sid,
          state,
          group: groupValue,
          ...(Number.isFinite(maxRestartNum) ? { max_restart: maxRestartNum } : {}),
          ...(Number.isFinite(maxRelocateNum) ? { max_relocate: maxRelocateNum } : {}),
          failback: failback ? 1 : 0,
          comment,
        },
        { onSuccess: props.onSuccess },
      );
    }
  };

  const isPending = props.mode === "create" ? createMut.isPending : updateMut.isPending;
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
              <SelectItem key={s} value={s}>{s}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <div className="space-y-2">
        <Label>Group</Label>
        <Select value={group === "" ? "__none__" : group} onValueChange={setGroup}>
          <SelectTrigger>
            <SelectValue placeholder="— None —" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="__none__">— None —</SelectItem>
            {groups.map((g) => (
              <SelectItem key={g.group} value={g.group}>{g.group}</SelectItem>
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
            max="10"
            value={maxRestart}
            onChange={(e) => { setMaxRestart(e.target.value); }}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="max-relocate">Max Relocate</Label>
          <Input
            id="max-relocate"
            type="number"
            min="0"
            max="10"
            value={maxRelocate}
            onChange={(e) => { setMaxRelocate(e.target.value); }}
          />
        </div>
      </div>

      <div className="flex items-center justify-between rounded-md border p-3">
        <div>
          <Label htmlFor="failback" className="cursor-pointer">Failback</Label>
          <p className="text-xs text-muted-foreground">Move back to higher-priority node when available.</p>
        </div>
        <Switch id="failback" checked={failback} onCheckedChange={setFailback} />
      </div>

      <div className="space-y-2">
        <Label htmlFor="comment">Comment</Label>
        <Textarea
          id="comment"
          value={comment}
          onChange={(e) => { setComment(e.target.value); }}
          rows={2}
        />
      </div>

      {mutError && (
        <div className="flex items-start gap-2 rounded-md border border-destructive bg-destructive/10 p-3 text-sm text-destructive">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <span>{describeHAResourceError(mutError)}</span>
        </div>
      )}

      <Button type="submit" disabled={isPending || (props.mode === "create" && !sid)}>
        {isPending ? "Saving..." : props.mode === "create" ? "Create" : "Save"}
      </Button>
    </form>
  );
}
