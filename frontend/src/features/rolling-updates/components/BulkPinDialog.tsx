import { useEffect, useRef, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import {
  Loader2,
  CheckCircle,
  XCircle,
  ShieldAlert,
  ShieldCheck,
  Circle,
} from "lucide-react";
import { apiClient } from "@/lib/api-client";
import { useQueryClient } from "@tanstack/react-query";
import type { SSHTestResponse, SSHKnownHost } from "@/types/api";

interface BulkPinDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  clusterId: string;
  nodes: { name: string; address: string }[];
}

type ScanState =
  | { kind: "idle" }
  | { kind: "scanning" }
  | { kind: "pending"; fingerprint: string; host: string; port: number }
  | { kind: "mismatch"; expectedFingerprint: string; presentedFingerprint: string }
  | { kind: "already_pinned"; fingerprint?: string }
  | { kind: "error"; message: string };

interface NodeRow {
  name: string;
  address: string;
  state: ScanState;
  selected: boolean;
  pinResult: "idle" | "pinning" | "pinned" | "error";
  pinError?: string;
}

export function BulkPinDialog({
  open,
  onOpenChange,
  clusterId,
  nodes,
}: BulkPinDialogProps) {
  const qc = useQueryClient();
  const [rows, setRows] = useState<NodeRow[]>([]);
  const [phase, setPhase] = useState<"scanning" | "review" | "pinning" | "done">(
    "scanning",
  );
  // Cross-closure cancellation: stored in a ref so the async IIFE below
  // can observe re-renders / unmount without ESLint flagging the read as
  // "always falsy" (which it would for a plain `let`).
  const cancelRef = useRef(false);

  useEffect(() => {
    if (!open) return;
    setPhase("scanning");
    setRows(
      nodes.map((n) => ({
        name: n.name,
        address: n.address,
        state: { kind: "scanning" },
        selected: true,
        pinResult: "idle",
      })),
    );

    cancelRef.current = false;
    void (async () => {
      // Scan sequentially so we don't overwhelm the cluster or the local
      // process; ≤10 nodes scan in well under 10 seconds.
      for (const [i, n] of nodes.entries()) {
        try {
          const result = await apiClient.post<SSHTestResponse>(
            `/api/v1/clusters/${clusterId}/ssh-credentials/test`,
            { node_name: n.name },
          );
          if (cancelRef.current) return;
          setRows((prev) =>
            prev.map((row, idx) => {
              if (idx !== i) return row;
              if (result.success) {
                return {
                  ...row,
                  state: result.fingerprint
                    ? { kind: "already_pinned", fingerprint: result.fingerprint }
                    : { kind: "already_pinned" },
                  selected: false,
                };
              }
              if (result.host_key_pending) {
                return {
                  ...row,
                  state: {
                    kind: "pending",
                    fingerprint: result.host_key_pending.fingerprint,
                    host: result.host_key_pending.host,
                    port: result.host_key_pending.port,
                  },
                };
              }
              if (result.host_key_mismatch) {
                return {
                  ...row,
                  state: {
                    kind: "mismatch",
                    expectedFingerprint:
                      result.host_key_mismatch.expected_fingerprint,
                    presentedFingerprint:
                      result.host_key_mismatch.presented_fingerprint,
                  },
                  selected: false,
                };
              }
              return {
                ...row,
                state: { kind: "error", message: result.message },
                selected: false,
              };
            }),
          );
        } catch (err) {
          if (cancelRef.current) return;
          const msg = err instanceof Error ? err.message : "Scan failed";
          setRows((prev) =>
            prev.map((row, idx) =>
              idx === i
                ? {
                    ...row,
                    state: { kind: "error", message: msg },
                    selected: false,
                  }
                : row,
            ),
          );
        }
      }
      if (!cancelRef.current) setPhase("review");
    })();

    return () => {
      cancelRef.current = true;
    };
  }, [open, clusterId, nodes]);

  const toggleRow = (idx: number) => {
    setRows((prev) =>
      prev.map((row, i) => (i === idx ? { ...row, selected: !row.selected } : row)),
    );
  };

  const selectableCount = rows.filter(
    (r) => r.state.kind === "pending" || r.state.kind === "mismatch",
  ).length;
  const checkedCount = rows.filter(
    (r) => r.selected && (r.state.kind === "pending" || r.state.kind === "mismatch"),
  ).length;

  const handlePinAll = () => {
    setPhase("pinning");
    void (async () => {
      for (const [i, row] of rows.entries()) {
        if (!row.selected) continue;
        if (row.state.kind !== "pending" && row.state.kind !== "mismatch") {
          continue;
        }
        const expectedFingerprint =
          row.state.kind === "pending"
            ? row.state.fingerprint
            : row.state.presentedFingerprint;

        setRows((prev) =>
          prev.map((r, idx) =>
            idx === i ? { ...r, pinResult: "pinning" } : r,
          ),
        );
        try {
          await apiClient.post<SSHKnownHost>(
            `/api/v1/clusters/${clusterId}/ssh-known-hosts`,
            {
              node_name: row.name,
              expected_fingerprint: expectedFingerprint,
            },
          );
          if (cancelRef.current) return;
          setRows((prev) =>
            prev.map((r, idx) =>
              idx === i ? { ...r, pinResult: "pinned" } : r,
            ),
          );
        } catch (err) {
          if (cancelRef.current) return;
          const msg = err instanceof Error ? err.message : "Pin failed";
          setRows((prev) =>
            prev.map((r, idx) =>
              idx === i ? { ...r, pinResult: "error", pinError: msg } : r,
            ),
          );
        }
      }
      if (!cancelRef.current) {
        void qc.invalidateQueries({ queryKey: ["ssh-known-hosts", clusterId] });
        setPhase("done");
      }
    })();
  };

  const renderStateIcon = (row: NodeRow) => {
    if (row.pinResult === "pinning") {
      return <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />;
    }
    if (row.pinResult === "pinned") {
      return <CheckCircle className="h-4 w-4 text-green-500" />;
    }
    if (row.pinResult === "error") {
      return <XCircle className="h-4 w-4 text-destructive" />;
    }
    switch (row.state.kind) {
      case "scanning":
        return <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />;
      case "already_pinned":
        return <ShieldCheck className="h-4 w-4 text-green-500" />;
      case "pending":
        return <ShieldAlert className="h-4 w-4 text-amber-500" />;
      case "mismatch":
        return <ShieldAlert className="h-4 w-4 text-destructive" />;
      case "error":
        return <XCircle className="h-4 w-4 text-destructive" />;
      default:
        return <Circle className="h-4 w-4 text-muted-foreground" />;
    }
  };

  const renderStateLabel = (row: NodeRow) => {
    if (row.pinResult === "pinning") return "Pinning…";
    if (row.pinResult === "pinned") return "Pinned ✓";
    if (row.pinResult === "error") return row.pinError ?? "Pin failed";
    switch (row.state.kind) {
      case "scanning":
        return "Scanning…";
      case "already_pinned":
        return "Already pinned";
      case "pending":
        return row.state.fingerprint;
      case "mismatch":
        return `MISMATCH — was ${row.state.expectedFingerprint}, now ${row.state.presentedFingerprint}`;
      case "error":
        return row.state.message;
      default:
        return "";
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Pin SSH host keys</DialogTitle>
          <DialogDescription>
            Compare each fingerprint to the value reported by the node itself
            (run <code className="text-xs">ssh-keyscan {"<node-ip>"}</code> on a
            trusted machine, or check the node console). Uncheck any nodes you
            don&apos;t want to trust right now.
          </DialogDescription>
        </DialogHeader>

        <div className="max-h-96 space-y-1 overflow-y-auto rounded-md border bg-muted/20 p-2">
          {rows.map((row, idx) => {
            const isSelectable =
              row.state.kind === "pending" || row.state.kind === "mismatch";
            return (
              <div
                key={row.name}
                className="flex items-start gap-2 rounded px-2 py-1.5 text-xs hover:bg-muted/50"
              >
                <input
                  type="checkbox"
                  className="mt-1"
                  checked={row.selected && isSelectable}
                  disabled={!isSelectable || phase !== "review"}
                  onChange={() => {
                    toggleRow(idx);
                  }}
                />
                <div className="mt-0.5 flex-shrink-0">{renderStateIcon(row)}</div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-baseline gap-2">
                    <span className="font-medium">{row.name}</span>
                    <span className="text-[10px] text-muted-foreground">
                      {row.address || "(no address)"}
                    </span>
                  </div>
                  <div className="break-all font-mono text-[10px] text-muted-foreground">
                    {renderStateLabel(row)}
                  </div>
                </div>
              </div>
            );
          })}
        </div>

        <DialogFooter>
          {phase === "scanning" && (
            <Button variant="outline" disabled>
              <Loader2 className="mr-2 h-4 w-4 animate-spin" />
              Scanning {rows.filter((r) => r.state.kind !== "scanning").length}/
              {rows.length}…
            </Button>
          )}
          {phase === "review" && selectableCount === 0 && (
            <Button
              onClick={() => {
                onOpenChange(false);
              }}
            >
              Done
            </Button>
          )}
          {phase === "review" && selectableCount > 0 && (
            <>
              <Button
                variant="outline"
                onClick={() => {
                  onOpenChange(false);
                }}
              >
                Cancel
              </Button>
              <Button
                onClick={handlePinAll}
                disabled={checkedCount === 0}
              >
                Trust &amp; Pin{" "}
                {checkedCount > 0
                  ? `${String(checkedCount)} of ${String(selectableCount)}`
                  : ""}
              </Button>
            </>
          )}
          {phase === "pinning" && (
            <Button variant="outline" disabled>
              <Loader2 className="mr-2 h-4 w-4 animate-spin" />
              Pinning…
            </Button>
          )}
          {phase === "done" && (
            <Button
              onClick={() => {
                onOpenChange(false);
              }}
            >
              Done
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
