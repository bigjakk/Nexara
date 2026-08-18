import {
  DISK_IMAGE_FORMATS,
  storageSupportsFormatChoice,
} from "@/features/storage/types/storage";

/**
 * Parse the bandwidth-limit field. Blank means "storage default" (0); anything
 * that isn't a whole non-negative number is rejected rather than silently
 * coerced, since a bad value here would throttle a multi-hour copy.
 */
export function parseBwlimit(text: string): {
  value: number;
  invalid: boolean;
} {
  if (text === "") return { value: 0, invalid: false };
  const n = Number(text);
  return {
    value: n,
    invalid: !Number.isInteger(n) || n < 0,
  };
}

/**
 * Resolve which format a move should request. Block-backed targets only hold
 * raw, so nothing is sent for them. Otherwise an untouched field (null) falls
 * back to the source format — sending nothing would let Proxmox allocate in
 * the target storage's default format and silently convert the image.
 */
export function resolveDiskFormat(
  chosen: string | null,
  sourceFormat: string | undefined,
  targetStorageType: string | undefined,
): string {
  if (!storageSupportsFormatChoice(targetStorageType)) return "";
  if (chosen !== null) return chosen;
  return DISK_IMAGE_FORMATS.some((f) => f.value === sourceFormat)
    ? (sourceFormat ?? "")
    : "";
}
