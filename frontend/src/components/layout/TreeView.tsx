import { useState } from "react";
import { ListFilter, X } from "lucide-react";
import { Segmented, type SegmentedOption } from "@/components/ui/segmented";
import { normalizeTreeFilter } from "@/lib/tree-filter";
import { cn } from "@/lib/utils";
import { useSidebarStore, type TreePerspective } from "@/stores/sidebar-store";
import { FavoritesSection } from "@/features/favorites/components/FavoritesSection";
import { InventoryTree } from "./InventoryTree";
import { StorageTree } from "./StorageTree";
import { VMTree } from "./VMTree";

/** The label is the grouping; the hover hint says what it holds. */
const PERSPECTIVES: SegmentedOption<TreePerspective>[] = [
  { value: "hosts", label: "Hosts", title: "Hosts and VMs" },
  { value: "vms", label: "Folders", title: "VMs and templates" },
  { value: "storage", label: "Storage", title: "Storage" },
];

/**
 * Narrows the tree to the rows whose name holds what is typed. It only looks
 * at what the tree has loaded: a collapsed cluster stays as it is until it is
 * opened.
 */
function TreeFilterBox({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <div className="relative">
      <ListFilter
        aria-hidden="true"
        className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
      />
      <input
        type="text"
        aria-label="Filter the tree by name"
        placeholder="Filter by name"
        autoComplete="off"
        spellCheck={false}
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
        }}
        onKeyDown={(e) => {
          if (e.key === "Escape" && value !== "") {
            e.stopPropagation();
            onChange("");
          }
        }}
        className="h-7 w-full rounded-lg bg-foreground/[0.07] pl-7 pr-7 text-[13px] outline-hidden placeholder:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring"
      />
      {value !== "" && (
        <button
          type="button"
          aria-label="Clear the filter"
          onClick={() => {
            onChange("");
          }}
          className="absolute right-1 top-1/2 grid h-5 w-5 -translate-y-1/2 place-items-center rounded-md text-muted-foreground outline-hidden hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
        >
          <X aria-hidden="true" className="h-3.5 w-3.5" />
        </button>
      )}
    </div>
  );
}

/**
 * The inventory tree with its grouping switch and filter box pinned above it.
 * Give it a bounded height (`flex-1`, a `max-h-*`) and the list scrolls inside.
 */
export function TreeView({ className }: { className?: string }) {
  const perspective = useSidebarStore((s) => s.perspective);
  const setPerspective = useSidebarStore((s) => s.setPerspective);
  // Not stored: a filter left on from last week would hide most of the estate
  // from someone who has forgotten it.
  const [query, setQuery] = useState("");
  const filter = normalizeTreeFilter(query);

  return (
    <div className={cn("flex min-h-0 flex-col", className)}>
      <div className="shrink-0 space-y-1.5 px-2.5 pb-2 pt-2.5">
        <Segmented
          aria-label="Group the tree by"
          value={perspective}
          onValueChange={setPerspective}
          options={PERSPECTIVES}
          className="flex [&>button]:flex-1"
        />
        <TreeFilterBox value={query} onChange={setQuery} />
      </div>
      {/* VMTree's drag-to-folder auto-scroll finds its scroller by this attribute. */}
      <div data-tree-scroller className="min-h-0 flex-1 overflow-y-auto pb-2">
        <FavoritesSection />
        {perspective === "hosts" && <InventoryTree filter={filter} />}
        {perspective === "vms" && <VMTree filter={filter} />}
        {perspective === "storage" && <StorageTree filter={filter} />}
      </div>
    </div>
  );
}
