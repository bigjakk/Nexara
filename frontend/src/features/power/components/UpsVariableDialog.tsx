import { useId, useState } from "react";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { describeError } from "@/lib/api-error";
import { useSetUpsVariable, useUpsCommandRuns } from "../api/ups-queries";
import { reportUpsRun } from "../lib/ups-runs";
import type { UpsDevice, UpsWritableVar } from "../types/ups";

/**
 * Sets one writable variable, with the editor its type calls for: a choice for
 * an ENUM, a bounded number for a RANGE, a text box within STRING:n. A
 * variable that can change the power the UPS supplies says so and takes a
 * confirmation (the UPS's name typed for the flag that lets the driver cut
 * it). Once sent, the dialog follows the run to the driver's answer.
 */
export function UpsVariableDialog({
  device,
  variable,
  onClose,
}: {
  device: UpsDevice;
  variable: UpsWritableVar;
  onClose: () => void;
}) {
  const id = useId();
  const [value, setValue] = useState(variable.value);
  const [understood, setUnderstood] = useState(false);
  const [typed, setTyped] = useState("");
  const [runId, setRunId] = useState<string | null>(null);
  const set = useSetUpsVariable(device.id);
  const settle = useSaveOutcome(true);
  const run = useUpsCommandRuns(device.id).data?.find((r) => r.id === runId);
  const confirmed =
    variable.confirm === "typed" ? typed === device.name : variable.confirm === "plain" ? understood : true;
  const range = variable.ranges?.[0];

  const save = () => {
    settle(
      set.mutateAsync({ name: variable.name, value, ...(variable.confirm === "typed" ? { confirm: typed } : {}) }),
      {
        action: `Setting ${variable.name} on ${device.name}`,
        onSuccess: (r) => {
          setRunId(r.id);
        },
        // Closed before upsd answered: say how it went somewhere.
        onLateSuccess: reportUpsRun,
        onError: () => undefined, // shown below from the mutation's state
      },
    );
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="break-all font-mono text-base">{variable.name}</DialogTitle>
          <DialogDescription>
            Set on {device.name} through {device.server_name}. It now reads <span className="font-mono">{variable.value || "(empty)"}</span>.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor={id}>New value</Label>
            {variable.type === "enum" ? (
              <Select value={value} onValueChange={setValue} disabled={runId !== null}>
                <SelectTrigger id={id}>
                  <SelectValue placeholder="Choose a value" />
                </SelectTrigger>
                <SelectContent>
                  {(variable.enum ?? []).map((v) => (
                    <SelectItem key={v} value={v}>
                      {v}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <Input
                id={id}
                value={value}
                disabled={runId !== null}
                {...(variable.type === "range" && range !== undefined
                  ? { type: "number", min: range[0], max: range[1], step: 1 }
                  : {})}
                {...(variable.max_len === undefined ? {} : { maxLength: variable.max_len })}
                onChange={(e) => {
                  setValue(e.target.value);
                }}
              />
            )}
            {variable.type === "range" && (variable.ranges ?? []).length > 0 && (
              <p className="text-xs text-muted-foreground">
                upsd allows {(variable.ranges ?? []).map(([lo, hi]) => `${String(lo)}–${String(hi)}`).join(" or ")}.
              </p>
            )}
          </div>
          {variable.affects_power && (
            <p className="flex items-start gap-1.5 text-sm text-amber-700 dark:text-amber-400">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
              This can change the power {device.name} supplies, or when upsmon shuts machines down: a battery-low point set too low
              means no warning before the battery is flat.
            </p>
          )}
          {variable.confirm === "plain" && (
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={understood}
                disabled={runId !== null}
                onCheckedChange={(v) => {
                  setUnderstood(v === true);
                }}
              />
              I understand what this changes
            </label>
          )}
          {variable.confirm === "typed" && (
            <div className="space-y-1.5">
              <Label htmlFor={`${id}-confirm`}>
                Type <span className="font-mono font-semibold">{device.name}</span> to confirm
              </Label>
              <Input
                id={`${id}-confirm`}
                autoComplete="off"
                spellCheck={false}
                value={typed}
                disabled={runId !== null}
                onChange={(e) => {
                  setTyped(e.target.value);
                }}
              />
            </div>
          )}
          {set.isError && <p className="text-sm text-destructive">{describeError(set.error)}</p>}
          {runId !== null && (
            <p role="status" className={run?.status === "failed" ? "text-sm text-destructive" : "text-sm"}>
              {run === undefined || run.status === "sent" || run.status === "pending"
                ? "Sent. Waiting for the driver's answer…"
                : run.status === "succeeded"
                  ? `Set.${run.result === "" ? "" : ` ${run.result}`}`
                  : `${run.status === "failed" ? "Failed" : "Not confirmed"}: ${run.result}`}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {runId === null ? "Cancel" : "Close"}
          </Button>
          {runId === null && (
            <Button disabled={!confirmed || value === variable.value || set.isPending} onClick={save}>
              {set.isPending ? "Setting…" : "Set"}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
