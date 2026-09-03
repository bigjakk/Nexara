import { useMemo, useState } from "react";
import { Check, ChevronsUpDown, X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  useClusterNodes,
  useClusterVMs,
} from "@/features/clusters/api/cluster-queries";
import { cn } from "@/lib/utils";

interface GuestMultiSelectProps {
  id?: string;
  clusterId: string;
  /** Comma-separated VMID list, the form PVE stores it in. */
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
}

function parseList(value: string): string[] {
  return value
    .split(",")
    .map((v) => v.trim())
    .filter((v) => v !== "");
}

/**
 * Picks guests for a backup job by VMID. Falls back to a free-text VMID field
 * when the cluster's guests can't be listed, so a job can still be edited
 * against an unreachable or still-syncing cluster.
 */
export function GuestMultiSelect({
  id,
  clusterId,
  value,
  onChange,
  placeholder = "Select guests...",
}: GuestMultiSelectProps) {
  const [open, setOpen] = useState(false);
  const vmsQuery = useClusterVMs(clusterId);
  const nodesQuery = useClusterNodes(clusterId);

  const nodeNames = useMemo(() => {
    const map = new Map<string, string>();
    for (const node of nodesQuery.data ?? []) map.set(node.id, node.name);
    return map;
  }, [nodesQuery.data]);

  const guests = useMemo(
    () => [...(vmsQuery.data ?? [])].sort((a, b) => a.vmid - b.vmid),
    [vmsQuery.data],
  );

  const selected = parseList(value);

  function toggle(vmid: string) {
    const next = selected.includes(vmid)
      ? selected.filter((v) => v !== vmid)
      : [...selected, vmid];
    // Numeric order keeps the stored list stable regardless of click order.
    next.sort((a, b) => Number(a) - Number(b));
    onChange(next.join(","));
  }

  const nameFor = (vmid: string) =>
    guests.find((g) => String(g.vmid) === vmid)?.name;

  if (vmsQuery.isError || (vmsQuery.isSuccess && guests.length === 0)) {
    return (
      <Input
        id={id}
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
        }}
        placeholder="100,101,102"
      />
    );
  }

  return (
    <div className="space-y-2">
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            id={id}
            type="button"
            variant="outline"
            role="combobox"
            aria-expanded={open}
            disabled={vmsQuery.isLoading}
            className="w-full justify-between font-normal"
          >
            <span
              className={cn(selected.length === 0 && "text-muted-foreground")}
            >
              {selected.length === 0
                ? placeholder
                : `${String(selected.length)} guest${selected.length === 1 ? "" : "s"} selected`}
            </span>
            <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
          </Button>
        </PopoverTrigger>
        <PopoverContent
          className="w-(--radix-popover-trigger-width) p-0"
          align="start"
        >
          <Command>
            <CommandInput placeholder="Search by VMID or name..." />
            <CommandList>
              <CommandEmpty>No guests found</CommandEmpty>
              <CommandGroup>
                {guests.map((guest) => {
                  const vmid = String(guest.vmid);
                  return (
                    <CommandItem
                      key={guest.id}
                      value={`${vmid} ${guest.name}`}
                      onSelect={() => {
                        toggle(vmid);
                      }}
                    >
                      <Check
                        className={cn(
                          "mr-2 h-4 w-4 shrink-0",
                          selected.includes(vmid) ? "opacity-100" : "opacity-0",
                        )}
                      />
                      {/* The explicit spaces keep the accessible name
                          readable ("101 web VM pve1"), since adjacent spans
                          otherwise concatenate without separators. */}
                      <span className="mr-2 font-mono text-xs text-muted-foreground">
                        {vmid}
                      </span>{" "}
                      <span className="truncate">{guest.name}</span>{" "}
                      <span className="ml-auto flex shrink-0 items-center gap-2 pl-2 text-xs text-muted-foreground">
                        <span className="uppercase">
                          {guest.type === "lxc" ? "CT" : "VM"}
                        </span>{" "}
                        {nodeNames.get(guest.node_id) && (
                          <span>{nodeNames.get(guest.node_id)}</span>
                        )}
                      </span>
                    </CommandItem>
                  );
                })}
              </CommandGroup>
            </CommandList>
          </Command>
        </PopoverContent>
      </Popover>

      {selected.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {selected.map((vmid) => (
            <Badge key={vmid} variant="secondary" className="gap-1">
              <span className="font-mono">{vmid}</span>
              {nameFor(vmid) && (
                <span className="text-muted-foreground">{nameFor(vmid)}</span>
              )}
              <button
                type="button"
                aria-label={`Remove ${vmid}`}
                onClick={() => {
                  toggle(vmid);
                }}
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
