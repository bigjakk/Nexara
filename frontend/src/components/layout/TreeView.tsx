import { cn } from "@/lib/utils";
import { useSidebarStore, type TreePerspective } from "@/stores/sidebar-store";
import { FavoritesSection } from "@/features/favorites/components/FavoritesSection";
import { InventoryTree } from "./InventoryTree";
import { StorageTree } from "./StorageTree";
import { VMTree } from "./VMTree";

interface PerspectiveOption {
  value: TreePerspective;
  label: string;
  /** What the grouping holds, for the hover hint. */
  title: string;
}

const PERSPECTIVES: PerspectiveOption[] = [
  { value: "hosts", label: "Hosts", title: "Hosts & VMs" },
  { value: "vms", label: "Folders", title: "VMs & Templates" },
  { value: "storage", label: "Storage", title: "Storage" },
];

function PerspectiveToggle() {
  const perspective = useSidebarStore((s) => s.perspective);
  const setPerspective = useSidebarStore((s) => s.setPerspective);

  return (
    <div
      role="group"
      aria-label="Group the tree by"
      className="m-2 flex shrink-0 gap-0.5 rounded-lg bg-foreground/[0.06] p-0.5"
    >
      {PERSPECTIVES.map(({ value, label, title }) => (
        <button
          key={value}
          type="button"
          title={title}
          aria-pressed={perspective === value}
          onClick={() => {
            setPerspective(value);
          }}
          className={cn(
            "h-7 flex-1 rounded-md text-xs font-medium transition-colors focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring",
            perspective === value
              ? "bg-card text-foreground shadow-sm dark:bg-foreground/15"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          {label}
        </button>
      ))}
    </div>
  );
}

/**
 * The inventory tree with its grouping switch pinned above it. Give it a
 * bounded height (`flex-1`, a `max-h-*`) and the list scrolls inside.
 */
export function TreeView({ className }: { className?: string }) {
  const perspective = useSidebarStore((s) => s.perspective);

  return (
    <div className={cn("flex min-h-0 flex-col", className)}>
      <PerspectiveToggle />
      {/* VMTree's drag-to-folder auto-scroll finds its scroller by this attribute. */}
      <div data-tree-scroller className="min-h-0 flex-1 overflow-y-auto px-1">
        <FavoritesSection />
        {perspective === "hosts" && <InventoryTree />}
        {perspective === "vms" && <VMTree />}
        {perspective === "storage" && <StorageTree />}
      </div>
    </div>
  );
}
