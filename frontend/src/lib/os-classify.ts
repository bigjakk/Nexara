export type OSFamily = "windows" | "linux" | "bsd" | "macos" | "solaris" | "other" | "unknown";

// Distro IDs that the QEMU guest agent or Proxmox LXC config can report.
// Sourced from /etc/os-release `ID=` values across major distros plus the
// historical Proxmox LXC `ostype` set.
const linuxIDs = new Set([
  "linux",
  "gnu/linux",
  "debian",
  "ubuntu",
  "linuxmint",
  "mint",
  "elementary",
  "pop",
  "kali",
  "parrot",
  "raspbian",
  "devuan",
  "centos",
  "rhel",
  "redhat",
  "fedora",
  "rocky",
  "almalinux",
  "ol",
  "oracle",
  "amzn",
  "amazon",
  "scientific",
  "clearlinux",
  "arch",
  "archlinux",
  "manjaro",
  "endeavouros",
  "garuda",
  "alpine",
  "postmarketos",
  "opensuse",
  "opensuse-leap",
  "opensuse-tumbleweed",
  "sles",
  "suse",
  "gentoo",
  "funtoo",
  "nixos",
  "void",
  "slackware",
  "mageia",
  "openmandriva",
  "zorin",
  "deepin",
  "kde-neon",
  "neon",
  "mx",
  "antix",
]);

export function classifyOS(ostype: string): OSFamily {
  if (!ostype) return "unknown";
  const v = ostype.toLowerCase();
  // Guest agent on Windows reports id="mswindows"; Proxmox config uses
  // win10/win11/wxp/wvista/w2k/w2k3/w2k8 etc.
  if (
    v.startsWith("win") ||
    v.startsWith("w2k") ||
    v.startsWith("wxp") ||
    v.startsWith("wvista") ||
    v === "mswindows" ||
    v === "microsoft windows"
  ) {
    return "windows";
  }
  // Proxmox QEMU config Linux variants.
  if (v.startsWith("l24") || v.startsWith("l26")) return "linux";
  if (v === "solaris") return "solaris";
  if (v === "macos" || v === "darwin" || v === "mac os x") return "macos";
  if (v === "freebsd" || v === "openbsd" || v === "netbsd" || v === "dragonfly") {
    return "bsd";
  }
  if (linuxIDs.has(v)) return "linux";
  if (v === "other") return "other";
  return "unknown";
}
