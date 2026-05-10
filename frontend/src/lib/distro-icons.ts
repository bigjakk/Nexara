// Distro-specific icon lookup. Path data and brand colors come from
// simple-icons (CC0). Each distro's name and logo remain the trademark of
// the respective project; we use them in nominative fashion to identify the
// running guest OS, mirroring how virt-manager / Proxmox / cockpit-pcp
// render guest distros.
import {
  siAlmalinux,
  siAlpinelinux,
  siArchlinux,
  siCentos,
  siDebian,
  siElementary,
  siFedora,
  siFreebsd,
  siGentoo,
  siKalilinux,
  siLinuxmint,
  siManjaro,
  siNixos,
  siOpenbsd,
  siOpensuse,
  siPopos,
  siRaspberrypi,
  siRedhat,
  siRockylinux,
  siUbuntu,
  siZorin,
  type SimpleIcon,
} from "simple-icons";

// Map of guest-OS id (lowercase) → simple-icons entry. Keys include both
// the values that the QEMU guest agent reports (from /etc/os-release ID=)
// and the values Proxmox uses for LXC `ostype`.
const distroMap: Record<string, SimpleIcon> = {
  ubuntu: siUbuntu,
  debian: siDebian,
  fedora: siFedora,
  arch: siArchlinux,
  archlinux: siArchlinux,
  alpine: siAlpinelinux,
  opensuse: siOpensuse,
  "opensuse-leap": siOpensuse,
  "opensuse-tumbleweed": siOpensuse,
  suse: siOpensuse,
  sles: siOpensuse,
  rocky: siRockylinux,
  rockylinux: siRockylinux,
  alma: siAlmalinux,
  almalinux: siAlmalinux,
  rhel: siRedhat,
  redhat: siRedhat,
  centos: siCentos,
  kali: siKalilinux,
  manjaro: siManjaro,
  mint: siLinuxmint,
  linuxmint: siLinuxmint,
  nixos: siNixos,
  gentoo: siGentoo,
  pop: siPopos,
  popos: siPopos,
  zorin: siZorin,
  raspbian: siRaspberrypi,
  elementary: siElementary,
  freebsd: siFreebsd,
  openbsd: siOpenbsd,
};

/**
 * Returns a simple-icons entry matching the supplied ostype, or null when
 * no per-distro icon is known. Caller is expected to render the SVG with
 * the entry's `path` and color it with `#${hex}`.
 */
export function getDistroIcon(ostype: string): SimpleIcon | null {
  if (!ostype) return null;
  return distroMap[ostype.toLowerCase()] ?? null;
}
