/**
 * The cluster's settings, which the cluster page lists in groups under its
 * Configure tab. Each value is the ?tab= the setting had when it was a tab of
 * its own, so the links made then (the search bar's shortcuts, the DRS card's)
 * still land on it.
 */
export const CONFIGURE_GROUPS = [
  {
    label: "Cluster",
    items: [
      { value: "options", label: "Options" },
      { value: "pools", label: "Resource pools" },
      { value: "mappings", label: "Resource mappings" },
      { value: "guest-tools", label: "Guest tools" },
      { value: "metric-servers", label: "Metric servers" },
    ],
  },
  {
    label: "Availability",
    items: [
      { value: "ha", label: "High availability" },
      { value: "drs", label: "DRS" },
      { value: "replication", label: "Replication" },
    ],
  },
  {
    label: "Network",
    items: [
      { value: "networks", label: "Networks" },
      { value: "firewall", label: "Firewall" },
    ],
  },
  {
    label: "Security",
    items: [
      { value: "access", label: "Access control" },
      { value: "certificates", label: "Certificates" },
    ],
  },
] as const;

export type ConfigureSection =
  (typeof CONFIGURE_GROUPS)[number]["items"][number]["value"];

/** Where Configure opens until another setting has been chosen. */
export const DEFAULT_CONFIGURE_SECTION: ConfigureSection = "options";

const SECTIONS: ReadonlySet<string> = new Set(
  CONFIGURE_GROUPS.flatMap((group) => group.items.map((item) => item.value)),
);

/** Whether a ?tab= value names one of the settings under Configure. */
export function isConfigureSection(tab: string): tab is ConfigureSection {
  return SECTIONS.has(tab);
}
