import { HelpCircle } from "lucide-react";
import { cn } from "@/lib/utils";
import { classifyOS } from "@/lib/os-classify";
import { getDistroIcon } from "@/lib/distro-icons";

interface OSIconProps {
  ostype: string;
  className?: string;
}

// Linux/BSD/macOS icons defer to simple-icons brand paths when we know the
// distro (CC0 paths, brand colors). For generic "Linux" (l24/l26 from
// Proxmox config with no guest agent) we fall back to a hand-drawn Tux —
// original Tux by Larry Ewing (lewing@isc.tamu.edu, 1996), used per his
// permission terms. Windows VMs render a stylized 4-pane mark used in
// nominative fashion to identify Microsoft Windows guests, mirroring the
// convention used by Proxmox VE, libvirt/virt-manager, and similar tools.
export function OSIcon({ ostype, className }: OSIconProps) {
  const family = classifyOS(ostype);
  const baseClass = cn("h-4 w-4 shrink-0", className);

  // Distro-specific brand icon takes priority over the generic family icon.
  const distro = getDistroIcon(ostype);
  if (distro) {
    return (
      <svg
        viewBox="0 0 24 24"
        aria-label={distro.title}
        className={baseClass}
      >
        <path d={distro.path} fill={`#${distro.hex}`} />
      </svg>
    );
  }

  if (family === "windows") {
    return (
      <svg
        viewBox="0 0 24 24"
        aria-label="Windows"
        className={baseClass}
      >
        <path
          fill="#0078d4"
          d="M3 5.5L11 4.3v7.2H3V5.5zM12 4.2L21 3v8.5h-9V4.2zM3 12.5h8v7.2L3 18.5v-6zM12 12.5h9V21l-9-1.3v-7.2z"
        />
      </svg>
    );
  }

  if (family === "linux") {
    // Generic Tux fallback for Linux VMs whose distro we couldn't pin down
    // (e.g. Proxmox config says "l26" with no guest agent running).
    return (
      <svg
        viewBox="0 0 24 24"
        aria-label="Linux"
        className={baseClass}
      >
        <ellipse cx="9" cy="20.4" rx="2.6" ry="1.1" fill="#f7c52d" />
        <ellipse cx="15" cy="20.4" rx="2.6" ry="1.1" fill="#f7c52d" />
        <path
          fill="#1a1a1a"
          d="M12 2.2c-3 0-4.7 2.4-4.7 5.4 0 1.4.4 2.4.4 3.4 0 1.2-1.2 2.1-2.2 3.6C4.5 16.1 3.8 17.8 3.8 19.3c0 1.6 1.5 2.6 4 2.6 1 0 2-.2 2.6-.5.5.3 1.1.5 1.6.5s1.1-.2 1.6-.5c.6.3 1.6.5 2.6.5 2.5 0 4-1 4-2.6 0-1.5-.7-3.2-1.7-4.7-1-1.5-2.2-2.4-2.2-3.6 0-1 .4-2 .4-3.4 0-3-1.7-5.4-4.7-5.4z"
        />
        <ellipse cx="12" cy="15.4" rx="3.4" ry="4.2" fill="#ffffff" />
        <ellipse cx="10.2" cy="6.6" rx="1.25" ry="1.6" fill="#ffffff" />
        <ellipse cx="13.8" cy="6.6" rx="1.25" ry="1.6" fill="#ffffff" />
        <ellipse cx="10.5" cy="6.9" rx="0.45" ry="0.7" fill="#1a1a1a" />
        <ellipse cx="13.5" cy="6.9" rx="0.45" ry="0.7" fill="#1a1a1a" />
        <path fill="#f29900" d="M10.4 8.2 L13.6 8.2 L12 10 Z" />
      </svg>
    );
  }

  // Generic fallback for "other"/"unknown"/bsd-without-distro/macOS-no-id.
  return (
    <HelpCircle
      aria-label={family === "unknown" ? "OS unknown" : family}
      className={cn(baseClass, "text-muted-foreground")}
    />
  );
}
