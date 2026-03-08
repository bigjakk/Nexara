import { useState } from "react";
import { BookMarked, Save, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  DropdownMenuSeparator,
  DropdownMenuLabel,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { DashboardPreset } from "../lib/widget-registry";

interface DashboardPresetSelectorProps {
  activePreset: DashboardPreset;
  savedPresets: DashboardPreset[];
  onSelect: (preset: DashboardPreset) => void;
  onSave: (name: string) => void;
  onDelete: (name: string) => void;
  onReset: () => void;
}

export function DashboardPresetSelector({
  activePreset,
  savedPresets,
  onSelect,
  onSave,
  onDelete,
  onReset,
}: DashboardPresetSelectorProps) {
  const [saveDialogOpen, setSaveDialogOpen] = useState(false);
  const [presetName, setPresetName] = useState("");

  const handleSave = () => {
    const name = presetName.trim();
    if (name) {
      onSave(name);
      setSaveDialogOpen(false);
      setPresetName("");
    }
  };

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size="sm" variant="outline" className="gap-1">
            <BookMarked className="h-4 w-4" />
            {activePreset.name}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-56">
          <DropdownMenuLabel>Dashboard Presets</DropdownMenuLabel>
          <DropdownMenuSeparator />

          <DropdownMenuItem onClick={onReset}>
            Default
          </DropdownMenuItem>

          {savedPresets.length > 0 && <DropdownMenuSeparator />}

          {savedPresets.map((preset) => (
            <DropdownMenuItem
              key={preset.name}
              className="flex items-center justify-between"
            >
              <span
                className="flex-1 cursor-pointer"
                onClick={() => { onSelect(preset); }}
              >
                {preset.name}
              </span>
              <button
                onClick={(e) => {
                  e.stopPropagation();
                  onDelete(preset.name);
                }}
                className="ml-2 rounded p-0.5 text-muted-foreground hover:text-destructive"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            </DropdownMenuItem>
          ))}

          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={() => {
              setPresetName(activePreset.name === "Default" ? "" : activePreset.name);
              setSaveDialogOpen(true);
            }}
          >
            <Save className="mr-2 h-4 w-4" />
            Save Current as Preset
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={saveDialogOpen} onOpenChange={setSaveDialogOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>Save Dashboard Preset</DialogTitle>
            <DialogDescription>
              Give your dashboard layout a name to save it for later.
            </DialogDescription>
          </DialogHeader>
          <Input
            placeholder="Preset name"
            value={presetName}
            onChange={(e) => { setPresetName(e.target.value); }}
            onKeyDown={(e) => {
              if (e.key === "Enter") handleSave();
            }}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => { setSaveDialogOpen(false); }}>
              Cancel
            </Button>
            <Button
              onClick={handleSave}
              disabled={!presetName.trim()}
            >
              Save
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
