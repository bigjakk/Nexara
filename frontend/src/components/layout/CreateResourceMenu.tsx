import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { CreateResourceDropdownItems } from "./create-resource-actions";

// Top-bar "Create" dropdown. The actual dialogs + multi-cluster picker live in
// the globally-mounted CreateResourceDialogs; the items come from the shared
// create-resource-actions module so every entry point stays in sync.
export function CreateResourceMenu() {
  const { data: clusters } = useClusters();
  const hasClusters = (clusters?.length ?? 0) > 0;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="gap-1.5">
          <Plus className="h-4 w-4" />
          <span className="hidden sm:inline">Create</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <CreateResourceDropdownItems disabled={!hasClusters} />
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
