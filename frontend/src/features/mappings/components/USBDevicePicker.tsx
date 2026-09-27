import { Input } from "@/components/ui/input";
import type { NodeUSBDevice } from "@/features/vms/api/vm-queries";
import {
  deviceLabel,
  passthroughCandidates,
  usbDeviceId,
  usbPortPath,
  type USBPickMode,
} from "../lib/usb-mapping";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring";

interface USBDevicePickerProps {
  /** The control's id, for the caller's <Label htmlFor>. */
  id: string;
  mode: USBPickMode;
  /** The node's USB devices; undefined while they load or when they failed. */
  devices: NodeUSBDevice[] | undefined;
  /** A device id in "device" mode, a port path in "port" mode. */
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
  /** Marks a typed device id the caller refuses. */
  invalid?: boolean;
}

/**
 * Picks the host USB device a mapping entry passes through, by id (any port)
 * or by the device on one port, from what the node lists.
 *
 * With no device to list, a pick by id falls back to typing the id; a pick by
 * port cannot, since the entry needs the id of the device on the port, and
 * says why instead. The caller renders the label and any refusal.
 */
export function USBDevicePicker({
  id,
  mode,
  devices,
  value,
  onChange,
  disabled = false,
  invalid = false,
}: USBDevicePickerProps) {
  const candidates = passthroughCandidates(devices);
  const hasDeviceList = candidates.length > 0;

  if (mode === "device") {
    return hasDeviceList ? (
      <select
        id={id}
        className={selectClass}
        value={value}
        disabled={disabled}
        onChange={(e) => {
          onChange(e.target.value);
        }}
      >
        <option value="">Select a device...</option>
        {candidates.map((d) => (
          <option
            key={`${usbDeviceId(d)}-${String(d.busnum)}-${String(d.devnum)}`}
            value={usbDeviceId(d)}
          >
            {deviceLabel(d, usbDeviceId(d))} ({usbDeviceId(d)})
          </option>
        ))}
      </select>
    ) : (
      <Input
        id={id}
        value={value}
        disabled={disabled}
        onChange={(e) => {
          onChange(e.target.value.trim());
        }}
        placeholder="vendor:product (e.g. 1234:5678)"
        aria-invalid={invalid}
      />
    );
  }

  return hasDeviceList ? (
    <select
      id={id}
      className={selectClass}
      value={value}
      disabled={disabled}
      onChange={(e) => {
        onChange(e.target.value);
      }}
    >
      <option value="">Select a port...</option>
      {candidates.map((d) => (
        <option key={usbPortPath(d)} value={usbPortPath(d)}>
          {deviceLabel(d, usbDeviceId(d))} ({usbPortPath(d)})
        </option>
      ))}
    </select>
  ) : (
    <p className="text-xs text-muted-foreground">
      {devices === undefined
        ? "The node's USB devices are not available: still loading, or they could not be read."
        : "The node lists no USB device on a port that can be passed through."}
    </p>
  );
}
