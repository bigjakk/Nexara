/** Words an id spells out in capitals: HA, not Ha; API URL, not Api url. */
const ACRONYMS = new Set([
  "acl",
  "acme",
  "api",
  "cpu",
  "ct",
  "cve",
  "dns",
  "drs",
  "gpu",
  "ha",
  "id",
  "ip",
  "iso",
  "ldap",
  "mac",
  "nfs",
  "nut",
  "oidc",
  "osd",
  "pbs",
  "pci",
  "pg",
  "pve",
  "sdn",
  "ssh",
  "sso",
  "tls",
  "totp",
  "upid",
  "ups",
  "url",
  "usb",
  "uuid",
  "vm",
  "vmid",
  "vnc",
  "zfs",
]);

/** Words an id runs together that read as two. */
const SPELLED = new Map([
  ["ipset", "IP set"],
  ["ipsets", "IP sets"],
]);

/**
 * An id such as `firewall_alias` or `cluster_id` spelled out in sentence case
 * ("Firewall alias", "Cluster ID"), for a type or field a label table does not
 * list, so it sits beside the table's own sentence-case labels.
 */
export function spellOutKey(key: string): string {
  return key
    .split(/[_\s]+/)
    .filter(Boolean)
    .map((word, i) => {
      const lower = word.toLowerCase();
      if (ACRONYMS.has(lower)) return lower.toUpperCase();
      const spelled = SPELLED.get(lower);
      if (spelled !== undefined) return spelled;
      return i === 0 ? lower.charAt(0).toUpperCase() + lower.slice(1) : lower;
    })
    .join(" ");
}
