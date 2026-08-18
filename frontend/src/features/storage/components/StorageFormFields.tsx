import { useEffect, useMemo, useRef, useState } from "react";
import { Check, ChevronsUpDown, Loader2, RefreshCw, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { cn } from "@/lib/utils";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useISCSITargets } from "../api/storage-queries";

/** Pause after the last portal keystroke before a discovery scan fires. */
const SCAN_DEBOUNCE_MS = 600;

interface ISCSITargetFieldProps {
  id: string;
  clusterId: string;
  /** Current value of the portal field this scan discovers against. */
  portal: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string | undefined;
}

/**
 * Target IQN picker backed by Proxmox's iSCSI discovery, mirroring the PVE
 * GUI: type a portal, and the advertised targets fill the dropdown with the
 * first one selected. Typing a target by hand stays available throughout —
 * portals that refuse discovery (ACLs, CHAP-only) are still perfectly usable
 * storage, so a failed scan must never block the form.
 */
export function ISCSITargetField({
  id,
  clusterId,
  portal,
  value,
  onChange,
  placeholder,
}: ISCSITargetFieldProps) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [debouncedPortal, setDebouncedPortal] = useState(portal.trim());

  function handleOpenChange(next: boolean) {
    setOpen(next);
    if (!next) setSearch("");
  }

  // Debounced so scanning follows a typed-out portal, not each keystroke of one.
  useEffect(() => {
    const trimmed = portal.trim();
    const timer = setTimeout(() => { setDebouncedPortal(trimmed); }, SCAN_DEBOUNCE_MS);
    return () => { clearTimeout(timer); };
  }, [portal]);

  const scan = useISCSITargets(clusterId, debouncedPortal);
  // Memoised: a fresh [] each render would re-run the auto-select effect below.
  const targets = useMemo(() => scan.data ?? [], [scan.data]);

  // Auto-fill the first discovered target, as the PVE GUI does. The ref records
  // what the scan put there, which is what lets a corrected portal drop a
  // now-wrong target instead of silently keeping it — filling only when the
  // field is empty would leave the previous portal's IQN in place, and an
  // unadvertised target is wrong whether or not this portal offers a
  // replacement. A hand-typed IQN is never touched: a portal may legitimately
  // not advertise the target an operator knows is there (CHAP, ACLs).
  const scanFilled = useRef<string | null>(null);
  useEffect(() => {
    if (scan.data === undefined) return; // no scan has completed for this portal
    const current = value.trim();
    const first = targets[0]?.target ?? "";
    const staleScanPick =
      current !== "" &&
      current === scanFilled.current &&
      !targets.some((t) => t.target === current);
    if (current !== "" && !staleScanPick) return;
    // Also the loop guard: onChange writes a fresh params object every time, so
    // re-running with nothing to change would re-trigger this effect forever.
    if (first === current) return;
    scanFilled.current = first === "" ? null : first;
    onChange(first);
  }, [scan.data, targets, value, onChange]);

  const trimmedSearch = search.trim();
  const showManualEntry =
    trimmedSearch !== "" && !targets.some((t) => t.target === trimmedSearch);

  // fromScan marks a pick this portal advertised, so a later portal change may
  // replace it. Manual entries clear the marker and are left alone.
  function select(target: string, fromScan: boolean) {
    scanFilled.current = fromScan ? target : null;
    onChange(target);
    setSearch("");
    setOpen(false);
  }

  return (
    <div className="space-y-1.5">
      <div className="flex gap-2">
        <Popover open={open} onOpenChange={handleOpenChange}>
          <PopoverTrigger asChild>
            <Button
              id={id}
              variant="outline"
              role="combobox"
              aria-expanded={open}
              className="min-w-0 flex-1 justify-between font-normal"
            >
              <span className={cn("truncate", value === "" && "text-muted-foreground")}>
                {value === "" ? (placeholder ?? "Select or enter a target IQN") : value}
              </span>
              <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
            </Button>
          </PopoverTrigger>
          <PopoverContent className="w-(--radix-popover-trigger-width) p-0" align="start">
            <Command shouldFilter>
              <CommandInput
                placeholder="Search or type an IQN..."
                value={search}
                onValueChange={setSearch}
              />
              <CommandList>
                <CommandEmpty>
                  {scan.isFetching ? "Scanning portal..." : "No targets discovered"}
                </CommandEmpty>
                {targets.length > 0 && (
                  <CommandGroup heading="Discovered targets">
                    {targets.map((t) => (
                      <CommandItem
                        key={t.target}
                        value={t.target}
                        onSelect={() => { select(t.target, true); }}
                      >
                        <Check
                          className={cn(
                            "mr-2 h-4 w-4 shrink-0",
                            value === t.target ? "opacity-100" : "opacity-0",
                          )}
                        />
                        <span className="truncate font-mono text-xs">{t.target}</span>
                      </CommandItem>
                    ))}
                  </CommandGroup>
                )}
                {showManualEntry && (
                  <CommandGroup heading="Manual entry">
                    <CommandItem
                      value={trimmedSearch}
                      onSelect={() => { select(trimmedSearch, false); }}
                    >
                      <Check className="mr-2 h-4 w-4 shrink-0 opacity-0" />
                      <span className="truncate">
                        Use &quot;<span className="font-mono text-xs">{trimmedSearch}</span>&quot;
                      </span>
                    </CommandItem>
                  </CommandGroup>
                )}
              </CommandList>
            </Command>
          </PopoverContent>
        </Popover>
        <Button
          type="button"
          variant="outline"
          size="icon"
          title="Rescan portal"
          aria-label="Rescan portal"
          disabled={portal.trim() === "" || scan.isFetching}
          onClick={() => { void scan.refetch(); }}
        >
          {scan.isFetching ? (
            <Loader2 className="h-4 w-4 animate-spin" />
          ) : (
            <RefreshCw className="h-4 w-4" />
          )}
        </Button>
      </div>
      <ScanStatus
        portal={portal}
        isFetching={scan.isFetching}
        error={scan.error}
        targetCount={targets.length}
        hasResult={scan.data !== undefined}
      />
    </div>
  );
}

interface ScanStatusProps {
  portal: string;
  isFetching: boolean;
  error: unknown;
  targetCount: number;
  hasResult: boolean;
}

function ScanStatus({ portal, isFetching, error, targetCount, hasResult }: ScanStatusProps) {
  if (portal.trim() === "") {
    return (
      <p className="text-xs text-muted-foreground">
        Enter a portal to discover its targets.
      </p>
    );
  }
  if (isFetching) {
    return (
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Loader2 className="h-3 w-3 animate-spin" />
        Scanning portal for targets...
      </p>
    );
  }
  if (error) {
    return (
      <p className="text-xs text-amber-500">
        Discovery failed ({error instanceof Error ? error.message : "unknown error"}). Enter
        the target IQN manually.
      </p>
    );
  }
  if (hasResult && targetCount === 0) {
    return (
      <p className="text-xs text-amber-500">
        Portal advertised no targets. Enter the target IQN manually.
      </p>
    );
  }
  if (hasResult) {
    return (
      <p className="text-xs text-muted-foreground">
        {targetCount} target{targetCount === 1 ? "" : "s"} discovered on this portal.
      </p>
    );
  }
  return null;
}

interface NodeRestrictionFieldProps {
  id: string;
  clusterId: string;
  /** Comma-separated node names, the form Proxmox stores. */
  value: string;
  onChange: (value: string) => void;
}

/**
 * Node restriction picker over the cluster's actual nodes, replacing free-text
 * entry where a typo silently produced storage no node would ever mount.
 *
 * Falls back to a text input when the node list can't be loaded, so a cluster
 * that is unreachable at this moment doesn't cost the operator the field.
 */
export function NodeRestrictionField({ id, clusterId, value, onChange }: NodeRestrictionFieldProps) {
  const [open, setOpen] = useState(false);
  const nodesQuery = useClusterNodes(clusterId);

  const selected = value
    .split(",")
    .map((n) => n.trim())
    .filter(Boolean);

  const available = nodesQuery.data ?? [];

  function toggle(name: string) {
    const next = selected.includes(name)
      ? selected.filter((n) => n !== name)
      : [...selected, name];
    onChange(next.join(","));
  }

  if (nodesQuery.isError || (nodesQuery.isSuccess && available.length === 0)) {
    return (
      <Input
        id={id}
        value={value}
        onChange={(e) => { onChange(e.target.value); }}
        placeholder="node1,node2 (leave empty for all)"
      />
    );
  }

  return (
    <div className="space-y-2">
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            id={id}
            variant="outline"
            role="combobox"
            aria-expanded={open}
            disabled={nodesQuery.isLoading}
            className="w-full justify-between font-normal"
          >
            <span className={cn(selected.length === 0 && "text-muted-foreground")}>
              {selected.length === 0
                ? "All (no restrictions)"
                : `${String(selected.length)} node${selected.length === 1 ? "" : "s"} selected`}
            </span>
            <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-(--radix-popover-trigger-width) p-0" align="start">
          <Command>
            <CommandInput placeholder="Search nodes..." />
            <CommandList>
              <CommandEmpty>No nodes found</CommandEmpty>
              <CommandGroup>
                {available.map((node) => (
                  <CommandItem
                    key={node.id}
                    value={node.name}
                    onSelect={() => { toggle(node.name); }}
                  >
                    <Check
                      className={cn(
                        "mr-2 h-4 w-4 shrink-0",
                        selected.includes(node.name) ? "opacity-100" : "opacity-0",
                      )}
                    />
                    <span className="mr-2 truncate">{node.name}</span>
                    {node.status !== "online" && (
                      <span className="ml-auto text-xs text-muted-foreground">
                        {node.status}
                      </span>
                    )}
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>

      {selected.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {selected.map((name) => (
            <Badge key={name} variant="secondary" className="gap-1">
              {name}
              <button
                type="button"
                aria-label={`Remove ${name}`}
                onClick={() => { toggle(name); }}
                className="rounded-sm hover:text-destructive"
              >
                <X className="h-3 w-3" />
              </button>
            </Badge>
          ))}
        </div>
      )}
    </div>
  );
}
