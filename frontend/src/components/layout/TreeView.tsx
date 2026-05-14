import { Boxes, HardDrive, Layers } from "lucide-react";
import { cn } from "@/lib/utils";
import { useSidebarStore, type TreePerspective } from "@/stores/sidebar-store";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { InventoryTree } from "./InventoryTree";
import { StorageTree } from "./StorageTree";
import { VMTree } from "./VMTree";

interface PerspectiveOption {
  value: TreePerspective;
  label: string;
  Icon: React.ComponentType<{ className?: string }>;
}

const PERSPECTIVES: PerspectiveOption[] = [
  { value: "hosts", label: "Hosts & VMs", Icon: Boxes },
  { value: "vms", label: "VMs & Templates", Icon: Layers },
  { value: "storage", label: "Storage", Icon: HardDrive },
];

function PerspectiveToggle() {
  const { perspective, setPerspective } = useSidebarStore();

  return (
    <div className="flex items-center gap-0.5 border-b px-1 py-1">
      {PERSPECTIVES.map(({ value, label, Icon }) => (
        <Tooltip key={value}>
          <TooltipTrigger asChild>
            <button
              onClick={() => {
                setPerspective(value);
              }}
              aria-pressed={perspective === value}
              aria-label={label}
              className={cn(
                "flex h-7 w-7 items-center justify-center rounded-md transition-colors",
                perspective === value
                  ? "bg-accent text-accent-foreground"
                  : "text-muted-foreground hover:bg-accent/50 hover:text-accent-foreground",
              )}
            >
              <Icon className="h-3.5 w-3.5" />
            </button>
          </TooltipTrigger>
          <TooltipContent side="right">{label}</TooltipContent>
        </Tooltip>
      ))}
    </div>
  );
}

export function TreeView() {
  const { perspective } = useSidebarStore();

  return (
    <div className="flex flex-col">
      <PerspectiveToggle />
      {perspective === "hosts" && <InventoryTree />}
      {perspective === "vms" && <VMTree />}
      {perspective === "storage" && <StorageTree />}
    </div>
  );
}
