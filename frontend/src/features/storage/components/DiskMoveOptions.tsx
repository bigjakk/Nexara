import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  DISK_IMAGE_FORMATS,
  storageSupportsFormatChoice,
} from "@/features/storage/types/storage";
import {
  parseBwlimit,
  resolveDiskFormat,
} from "@/features/storage/lib/disk-move";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50";

export interface DiskMoveOptionsProps {
  /** Prefixes the field ids so several of these can share a page. */
  idPrefix: string;
  /**
   * Type of the selected target storage. Drives whether a format can be
   * chosen, mirroring how Proxmox greys the field out. Pass undefined when no
   * target is selected yet.
   */
  targetStorageType?: string | undefined;
  /** Omit the format field entirely — containers, or a live-only migration. */
  hideFormat?: boolean | undefined;
  /** Format the volume is in today, preselected when the target allows it. */
  sourceFormat?: string | undefined;
  /** null = untouched (use sourceFormat); "" = explicitly let the storage decide. */
  format?: string | null | undefined;
  onFormatChange?: ((value: string) => void) | undefined;
  /** Raw text of the bandwidth field, so a half-typed value stays editable. */
  bwlimit: string;
  onBwlimitChange: (value: string) => void;
  deleteSource: boolean;
  onDeleteSourceChange: (value: boolean) => void;
  deleteSourceLabel?: string | undefined;
  /** Shown when delete-source is off, naming what gets left behind. */
  keptHint?: string | undefined;
  /**
   * Omit the delete-source control entirely, for an operation that leaves no
   * source behind to delete.
   *
   * Worth hiding rather than showing-and-ignoring: the same switch elsewhere
   * deletes source volumes, and on a cross-cluster migration destroys the
   * source guest. A control that visibly does nothing in one dialog teaches
   * that it is harmless in the one where it is not. Each caller decides; see
   * `showDeleteSource` in the migration dialogs.
   */
  hideDeleteSource?: boolean | undefined;
}

/**
 * The options every storage move shares — format, bandwidth limit and whether
 * the source is deleted. Rendered by the per-disk move dialogs and by the
 * migration dialogs so the two can't drift apart again.
 */
export function DiskMoveOptions({
  idPrefix,
  targetStorageType,
  hideFormat = false,
  sourceFormat,
  format = null,
  onFormatChange,
  bwlimit,
  onBwlimitChange,
  deleteSource,
  onDeleteSourceChange,
  deleteSourceLabel = "Delete source after move completes",
  keptHint,
  hideDeleteSource = false,
}: DiskMoveOptionsProps) {
  const canChooseFormat = storageSupportsFormatChoice(targetStorageType);
  const effectiveFormat = resolveDiskFormat(
    format,
    sourceFormat,
    targetStorageType,
  );
  const { invalid: bwlimitInvalid } = parseBwlimit(bwlimit);

  return (
    <>
      {!hideFormat && (
        <div className="space-y-2">
          <Label htmlFor={`${idPrefix}-format`}>Format</Label>
          <select
            id={`${idPrefix}-format`}
            className={selectClass}
            value={effectiveFormat}
            disabled={!canChooseFormat}
            onChange={(e) => {
              onFormatChange?.(e.target.value);
            }}
          >
            <option value="">
              {canChooseFormat ? "Storage default" : "Set by target storage"}
            </option>
            {DISK_IMAGE_FORMATS.map((f) => (
              <option key={f.value} value={f.value}>
                {f.label}
              </option>
            ))}
          </select>
          {!canChooseFormat && targetStorageType !== undefined && (
            <p className="text-xs text-muted-foreground">
              Block-backed storage stores images as raw only.
            </p>
          )}
        </div>
      )}

      <div className="space-y-2">
        <Label htmlFor={`${idPrefix}-bwlimit`}>Bandwidth Limit (KiB/s)</Label>
        <Input
          id={`${idPrefix}-bwlimit`}
          type="number"
          min={0}
          value={bwlimit}
          onChange={(e) => {
            onBwlimitChange(e.target.value);
          }}
          placeholder="Unlimited"
        />
        {bwlimitInvalid && (
          <p className="text-xs text-destructive">
            Enter a whole number of KiB/s, or leave blank.
          </p>
        )}
      </div>

      {!hideDeleteSource && (
        <div className="space-y-1">
          <div className="flex items-center justify-between gap-4">
            <Label htmlFor={`${idPrefix}-delete-source`}>
              {deleteSourceLabel}
            </Label>
            <Switch
              id={`${idPrefix}-delete-source`}
              checked={deleteSource}
              onCheckedChange={onDeleteSourceChange}
            />
          </div>
          {!deleteSource && keptHint && (
            <p className="text-xs text-muted-foreground">{keptHint}</p>
          )}
        </div>
      )}
    </>
  );
}
