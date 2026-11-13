import { useState, useEffect, useCallback, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import {
  useGlobalSearch,
  type SearchResult,
} from "@/features/search/api/search-queries";
import { VMContextMenu } from "@/features/vms/components/VMContextMenu";
import { useAuth } from "@/hooks/useAuth";
import { usePermissions } from "@/hooks/usePermissions";
import { useThemeStore } from "@/stores/theme-store";
import { StatusIcon } from "@/components/StatusIcon";
import {
  useCreateResourceStore,
  type CreateKind,
} from "@/stores/create-resource-store";
import {
  Monitor,
  Server,
  HardDrive,
  BatteryCharging,
  Database,
  Search,
  Layers,
  Settings,
  Shield,
  Network,
  Repeat,
  Award,
  BarChart3,
  Bell,
  FileText,
  Map,
  Eye,
  Users,
  Key,
  Lock,
  Palette,
  Tag,
  Cpu,
  Globe,
  Container,
  TerminalSquare,
  Sun,
  Moon,
  MonitorCog,
  Plus,
  Upload,
  Usb,
} from "lucide-react";
import type { ReactNode } from "react";

// --- Static page definitions ---

interface PageEntry {
  /** Search keywords (lowercase) */
  keywords: string[];
  label: string;
  description: string;
  icon: ReactNode;
  /** If set, one entry per cluster using this template. Use {id} and {name} placeholders. */
  clusterPath?: string;
  /** Static path (no cluster context) */
  path?: string;
  /** "action:resource" permission required to see this entry */
  requiredPermission?: string;
  /** The permission must be held install-wide, as every UPS route takes it. */
  global?: boolean;
  /** Show in the zero-query "Go to" group */
  pinned?: boolean;
}

const GLOBAL_PAGES: PageEntry[] = [
  {
    keywords: ["dashboard", "home", "overview"],
    label: "Dashboard",
    description: "Main dashboard",
    icon: <BarChart3 className="h-4 w-4" />,
    path: "/",
    pinned: true,
  },
  {
    keywords: ["inventory", "vms", "containers", "virtual machines"],
    label: "Inventory",
    description: "All VMs & containers",
    icon: <Monitor className="h-4 w-4" />,
    path: "/inventory",
    pinned: true,
  },
  {
    keywords: ["storage", "disk", "volumes"],
    label: "Storage",
    description: "Storage pools",
    icon: <HardDrive className="h-4 w-4" />,
    path: "/storage",
    pinned: true,
  },
  {
    keywords: ["backup", "restore", "pbs"],
    label: "Backup",
    description: "Backup dashboard",
    icon: <FileText className="h-4 w-4" />,
    path: "/backup",
    pinned: true,
  },
  {
    keywords: ["topology", "map", "infrastructure", "diagram"],
    label: "Topology",
    description: "Infrastructure map",
    icon: <Map className="h-4 w-4" />,
    path: "/topology",
    pinned: true,
  },
  {
    keywords: ["alerts", "notifications", "rules", "channels"],
    label: "Alerts",
    description: "Alert rules & history",
    icon: <Bell className="h-4 w-4" />,
    path: "/alerts",
    requiredPermission: "view:alert",
    pinned: true,
  },
  {
    keywords: ["power", "ups", "nut", "battery", "outage"],
    label: "Power",
    description: "UPS devices and NUT servers",
    icon: <BatteryCharging className="h-4 w-4" />,
    path: "/power",
    requiredPermission: "view:ups",
    global: true,
  },
  {
    keywords: ["reports", "schedule", "generate"],
    label: "Reports",
    description: "Report schedules",
    icon: <FileText className="h-4 w-4" />,
    path: "/reports",
    requiredPermission: "view:report",
  },
  {
    keywords: ["events", "audit", "log", "syslog"],
    label: "Events",
    description: "Event log",
    icon: <Eye className="h-4 w-4" />,
    path: "/events",
    requiredPermission: "view:audit",
    pinned: true,
  },
  {
    keywords: [
      "security",
      "cve",
      "vulnerability",
      "scanning",
      "rolling",
      "update",
    ],
    label: "Security",
    description: "CVE scanning & rolling updates",
    icon: <Shield className="h-4 w-4" />,
    path: "/security",
    requiredPermission: "view:cve_scan",
    pinned: true,
  },
  {
    keywords: ["users", "admin", "accounts", "rbac"],
    label: "Admin: Users",
    description: "User management",
    icon: <Users className="h-4 w-4" />,
    path: "/admin/users",
    requiredPermission: "manage:user",
  },
  {
    keywords: ["roles", "permissions", "rbac", "admin"],
    label: "Admin: Roles",
    description: "Role management",
    icon: <Key className="h-4 w-4" />,
    path: "/admin/roles",
    requiredPermission: "manage:user",
  },
  {
    keywords: ["ldap", "active directory", "ad", "admin"],
    label: "Admin: LDAP",
    description: "LDAP/AD configuration",
    icon: <Globe className="h-4 w-4" />,
    path: "/admin/ldap",
    requiredPermission: "manage:user",
  },
  {
    keywords: ["oidc", "sso", "oauth", "admin"],
    label: "Admin: OIDC/SSO",
    description: "OIDC provider configuration",
    icon: <Lock className="h-4 w-4" />,
    path: "/admin/oidc",
    requiredPermission: "manage:user",
  },
  {
    keywords: ["branding", "logo", "title", "admin"],
    label: "Admin: Branding",
    description: "App branding & logo",
    icon: <Palette className="h-4 w-4" />,
    path: "/admin/branding",
    requiredPermission: "manage:user",
  },
  {
    keywords: ["appearance", "theme", "dark", "light", "accent", "color"],
    label: "Settings: Appearance",
    description: "Theme & display preferences",
    icon: <Palette className="h-4 w-4" />,
    path: "/settings/appearance",
  },
  {
    keywords: ["security", "totp", "2fa", "two factor", "mfa"],
    label: "Settings: Security",
    description: "Two-factor authentication",
    icon: <Shield className="h-4 w-4" />,
    path: "/settings/security",
  },
];

/** Per-cluster pages — one entry generated per cluster */
const CLUSTER_PAGES: PageEntry[] = [
  {
    keywords: ["drs", "scheduler", "resource", "balancing", "affinity"],
    label: "DRS",
    description: "Dynamic resource scheduler",
    icon: <Cpu className="h-4 w-4" />,
    clusterPath: "/clusters/{id}?tab=drs",
  },
  {
    keywords: ["firewall", "rules", "aliases", "ipset", "security group"],
    label: "Firewall",
    description: "Cluster firewall rules",
    icon: <Shield className="h-4 w-4" />,
    clusterPath: "/clusters/{id}?tab=firewall",
  },
  {
    keywords: ["ceph", "osd", "pool", "monitor", "rados"],
    label: "Ceph",
    description: "Ceph storage cluster",
    icon: <Database className="h-4 w-4" />,
    clusterPath: "/clusters/{id}?tab=ceph",
  },
  {
    keywords: ["network", "vnet", "sdn", "bridge", "vlan"],
    label: "Networks",
    description: "Cluster networking",
    icon: <Network className="h-4 w-4" />,
    clusterPath: "/clusters/{id}?tab=networks",
  },
  {
    keywords: [
      "options",
      "datacenter",
      "notes",
      "description",
      "settings",
      "config",
    ],
    label: "Options",
    description: "Datacenter options & notes",
    icon: <Settings className="h-4 w-4" />,
    clusterPath: "/clusters/{id}?tab=options",
  },
  {
    keywords: ["ha", "high availability", "failover", "fencing"],
    label: "HA",
    description: "High availability",
    icon: <Repeat className="h-4 w-4" />,
    clusterPath: "/clusters/{id}?tab=ha",
  },
  {
    keywords: ["pool", "resource pool"],
    label: "Pools",
    description: "Resource pools",
    icon: <Layers className="h-4 w-4" />,
    clusterPath: "/clusters/{id}?tab=pools",
  },
  {
    keywords: [
      "mapping",
      "resource mapping",
      "usb",
      "pci",
      "gpu",
      "passthrough",
      "device",
    ],
    label: "Resource Mappings",
    description: "USB and PCI device mappings",
    icon: <Usb className="h-4 w-4" />,
    clusterPath: "/clusters/{id}?tab=mappings",
  },
  {
    keywords: ["replication", "zfs", "sync", "replicate"],
    label: "Replication",
    description: "ZFS replication jobs",
    icon: <Repeat className="h-4 w-4" />,
    clusterPath: "/clusters/{id}?tab=replication",
  },
  {
    keywords: ["certificate", "acme", "letsencrypt", "ssl", "tls"],
    label: "Certificates",
    description: "ACME certificates",
    icon: <Award className="h-4 w-4" />,
    clusterPath: "/clusters/{id}?tab=certificates",
  },
  {
    keywords: ["metric", "influxdb", "graphite", "metrics server"],
    label: "Metric Servers",
    description: "External metric targets",
    icon: <BarChart3 className="h-4 w-4" />,
    clusterPath: "/clusters/{id}?tab=metric-servers",
  },
  {
    keywords: ["tags", "tag", "label", "registered"],
    label: "Tags",
    description: "Tag management",
    icon: <Tag className="h-4 w-4" />,
    clusterPath: "/clusters/{id}?tab=options",
  },
];

interface PageMatch {
  label: string;
  description: string;
  icon: ReactNode;
  path: string;
  clusterName?: string;
}

/** A result row's glyph: plain and muted, so only a state dot carries colour. */
function RowIcon({ children }: { children: ReactNode }) {
  return (
    <span className="flex h-4 w-4 shrink-0 items-center justify-center text-muted-foreground">
      {children}
    </span>
  );
}

/** Small print at a row's right edge: what kind of thing it is, and where. */
function RowAside({ children }: { children: ReactNode }) {
  return (
    <span className="ml-auto max-w-[55%] shrink-0 truncate pl-3 text-[12.5px] text-muted-foreground">
      {children}
    </span>
  );
}

function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="rounded-[5px] bg-foreground/[0.08] px-1.5 py-px font-mono text-[11px] text-muted-foreground">
      {children}
    </kbd>
  );
}

/** A result row: 36px, the selected one in the primary colour at low strength. */
const ROW_CLASS =
  "h-9 gap-2.5 rounded-md px-2.5 data-[selected='true']:bg-primary/[0.13] data-[selected=true]:text-foreground";

type PaletteView = "root" | "create-vm" | "create-ct" | "create-import";

const viewToKind: Partial<Record<PaletteView, CreateKind>> = {
  "create-vm": "vm",
  "create-ct": "ct",
  "create-import": "import",
};
const kindToView: Record<CreateKind, PaletteView> = {
  vm: "create-vm",
  ct: "create-ct",
  import: "create-import",
};

export function SearchBar() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [view, setView] = useState<PaletteView>("root");
  const navigate = useNavigate();
  const request = useCreateResourceStore((s) => s.request);
  const { hasPermission } = useAuth();
  const { hasGlobalPermission } = usePermissions();
  const setThemeMode = useThemeStore((s) => s.setMode);
  const searchQuery = useGlobalSearch(query);
  const { data: clusters } = useClusters();

  const can = useCallback(
    (perm?: string, global = false) => {
      if (!perm) return true;
      const parts = perm.split(":");
      return (global ? hasGlobalPermission : hasPermission)(parts[0] ?? "", parts[1] ?? "");
    },
    [hasPermission, hasGlobalPermission],
  );

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setOpen((prev) => !prev);
      }
    };
    document.addEventListener("keydown", handler);
    return () => {
      document.removeEventListener("keydown", handler);
    };
  }, []);

  const handleOpenChange = useCallback((next: boolean) => {
    setOpen(next);
    if (!next) {
      setQuery("");
      setView("root");
    }
  }, []);

  const close = useCallback(() => {
    handleOpenChange(false);
  }, [handleOpenChange]);

  const goTo = useCallback(
    (path: string) => {
      close();
      void navigate(path);
    },
    [close, navigate],
  );

  // Begin a create flow: single cluster goes straight to the dialog,
  // multiple clusters detour through an in-palette cluster picker view.
  const startCreate = useCallback(
    (type: CreateKind) => {
      const list = clusters ?? [];
      if (list.length === 1 && list[0]) {
        request(type, list[0].id);
        setOpen(false);
        setQuery("");
        setView("root");
        return;
      }
      setQuery("");
      setView(kindToView[type]);
    },
    [clusters, request],
  );

  const pickCreateCluster = useCallback(
    (clusterId: string) => {
      const kind = viewToKind[view];
      setOpen(false);
      setQuery("");
      setView("root");
      if (kind) request(kind, clusterId);
    },
    [view, request],
  );

  interface ActionEntry {
    id: string;
    label: string;
    description: string;
    keywords: string[];
    icon: ReactNode;
    perform: () => void;
  }

  const actions = useMemo<ActionEntry[]>(() => {
    const list: ActionEntry[] = [
      {
        id: "create-vm",
        label: "Create virtual machine…",
        description: "New QEMU guest",
        keywords: ["create", "new", "vm", "virtual machine", "qemu"],
        icon: <Plus className="h-4 w-4" />,
        perform: () => {
          startCreate("vm");
        },
      },
      {
        id: "create-ct",
        label: "Create container…",
        description: "New LXC guest",
        keywords: ["create", "new", "ct", "container", "lxc"],
        icon: <Container className="h-4 w-4" />,
        perform: () => {
          startCreate("ct");
        },
      },
    ];
    if (can("manage:vm_import")) {
      list.push({
        id: "import-vm",
        label: "Import virtual machine…",
        description: "From OVA/OVF or ESXi",
        keywords: ["import", "ova", "ovf", "esxi", "vmware", "migrate"],
        icon: <Upload className="h-4 w-4" />,
        perform: () => {
          startCreate("import");
        },
      });
    }
    list.push(
      {
        id: "console",
        label: "Open console",
        description: "Terminal & VNC sessions",
        keywords: ["console", "terminal", "shell", "vnc", "xterm"],
        icon: <TerminalSquare className="h-4 w-4" />,
        perform: () => {
          goTo("/console");
        },
      },
      {
        id: "theme-dark",
        label: "Theme: dark",
        description: "Switch to dark mode",
        keywords: ["theme", "dark", "mode", "appearance"],
        icon: <Moon className="h-4 w-4" />,
        perform: () => {
          setThemeMode("dark");
          close();
        },
      },
      {
        id: "theme-light",
        label: "Theme: light",
        description: "Switch to light mode",
        keywords: ["theme", "light", "mode", "appearance"],
        icon: <Sun className="h-4 w-4" />,
        perform: () => {
          setThemeMode("light");
          close();
        },
      },
      {
        id: "theme-system",
        label: "Theme: system",
        description: "Follow the OS preference",
        keywords: ["theme", "system", "auto", "mode", "appearance"],
        icon: <MonitorCog className="h-4 w-4" />,
        perform: () => {
          setThemeMode("system");
          close();
        },
      },
    );
    return list;
  }, [startCreate, goTo, setThemeMode, close, can]);

  const q = query.toLowerCase();

  const actionMatches = useMemo(
    () =>
      actions.filter(
        (a) =>
          q.length === 0 ||
          a.label.toLowerCase().includes(q) ||
          a.keywords.some((kw) => kw.includes(q)),
      ),
    [actions, q],
  );

  // Filter static pages by query
  const pageMatches = useMemo(() => {
    if (q.length < 2) return [];
    const matches: PageMatch[] = [];

    for (const page of GLOBAL_PAGES) {
      if (!can(page.requiredPermission, page.global)) continue;
      if (
        page.keywords.some((kw) => kw.includes(q)) ||
        page.label.toLowerCase().includes(q)
      ) {
        matches.push({
          label: page.label,
          description: page.description,
          icon: page.icon,
          path: page.path ?? "/",
        });
      }
    }

    const clusterList = clusters ?? [];
    for (const page of CLUSTER_PAGES) {
      if (
        page.keywords.some((kw) => kw.includes(q)) ||
        page.label.toLowerCase().includes(q)
      ) {
        for (const cluster of clusterList) {
          matches.push({
            label: page.label,
            description: page.description,
            icon: page.icon,
            path: (page.clusterPath ?? "").replace("{id}", cluster.id),
            clusterName: cluster.name,
          });
        }
      }
    }

    return matches;
  }, [q, clusters, can]);

  const pinnedPages = useMemo(
    () => GLOBAL_PAGES.filter((p) => p.pinned && can(p.requiredPermission, p.global)),
    [can],
  );

  const clusterMatches = useMemo(() => {
    const list = clusters ?? [];
    if (q.length >= 2) return [];
    return list.filter(
      (c) => q.length === 0 || c.name.toLowerCase().includes(q),
    );
  }, [clusters, q]);

  const handleSelect = useCallback(
    (result: SearchResult) => {
      close();
      switch (result.type) {
        case "vm":
        case "ct":
          void navigate(
            `/inventory/${result.type}/${result.cluster_id}/${result.id}`,
          );
          break;
        case "node":
          void navigate(`/clusters/${result.cluster_id}/nodes/${result.id}`);
          break;
        case "storage":
          void navigate(`/storage?cluster=${result.cluster_id}`);
          break;
        case "ups":
          void navigate(`/power/${result.id}`);
          break;
        default:
          void navigate(`/clusters/${result.cluster_id}`);
          break;
      }
    },
    [close, navigate],
  );

  const getIcon = (type: string) => {
    switch (type) {
      case "vm":
        return <Monitor className="h-4 w-4" />;
      case "ct":
        return <Container className="h-4 w-4" />;
      case "node":
        return <Server className="h-4 w-4" />;
      case "storage":
        return <HardDrive className="h-4 w-4" />;
      case "cluster":
        return <Layers className="h-4 w-4" />;
      case "ups":
        return <BatteryCharging className="h-4 w-4" />;
      default:
        return null;
    }
  };

  const results = searchQuery.data ?? [];
  const grouped = {
    vms: results.filter((r) => r.type === "vm" || r.type === "ct"),
    nodes: results.filter((r) => r.type === "node"),
    storage: results.filter((r) => r.type === "storage"),
    clusters: results.filter((r) => r.type === "cluster"),
    ups: results.filter((r) => r.type === "ups"),
  };

  const hasResults =
    grouped.vms.length > 0 ||
    grouped.nodes.length > 0 ||
    grouped.storage.length > 0 ||
    grouped.clusters.length > 0 ||
    grouped.ups.length > 0 ||
    pageMatches.length > 0 ||
    actionMatches.length > 0 ||
    clusterMatches.length > 0 ||
    q.length < 2;

  const inPicker = view !== "root";

  /** "VM 101", "CT 204": what a guest row's small print opens with. */
  const guestKind = (r: SearchResult) =>
    `${r.type === "ct" ? "CT" : "VM"}${r.vmid ? ` ${String(r.vmid)}` : ""}`;
  const location = (...parts: (string | undefined)[]) =>
    parts.filter((p) => p !== undefined && p !== "").join(" · ");

  return (
    <>
      <button
        aria-label="Search or jump to…"
        onClick={() => {
          setOpen(true);
        }}
        className="flex h-9 w-full max-w-64 items-center gap-2 rounded-lg bg-foreground/[0.07] px-3 text-sm text-muted-foreground outline-hidden transition-colors hover:bg-foreground/[0.1] hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring md:max-w-md"
      >
        <Search className="h-4 w-4 shrink-0" />
        <span className="min-w-0 flex-1 truncate text-left">
          <span className="sm:hidden">Search</span>
          <span className="hidden sm:inline">Search or jump to…</span>
        </span>
        <kbd className="pointer-events-none hidden select-none rounded-[5px] bg-foreground/[0.08] px-1.5 py-px font-mono text-[11px] sm:block">
          Ctrl+K
        </kbd>
      </button>

      <Dialog open={open} onOpenChange={handleOpenChange}>
        {/* The dialog's own close button is hidden: Escape, the scrim and
            the footer's hint close the palette, and the button sat on the
            input. */}
        <DialogContent className="top-[14%] max-h-[calc(86vh-1rem)] max-w-[calc(100%-1.5rem)] translate-y-0 gap-0 overflow-hidden rounded-xl border-0 bg-card p-0 sm:max-w-[660px] [&>button:last-child]:hidden">
          <DialogTitle className="sr-only">Command palette</DialogTitle>
          <Command
            shouldFilter={false}
            onKeyDown={(e) => {
              if (inPicker && e.key === "Backspace" && query === "") {
                e.preventDefault();
                setView("root");
              }
            }}
            className="rounded-none bg-transparent [&_[cmdk-group-heading]]:px-2.5 [&_[cmdk-group-heading]]:pb-1 [&_[cmdk-group-heading]]:pt-2.5 [&_[cmdk-group-heading]]:text-[12.5px] [&_[cmdk-group-heading]]:font-normal [&_[cmdk-group]]:px-2 [&_[cmdk-group]]:py-0 [&_[cmdk-input-wrapper]]:px-[18px] [&_[cmdk-input-wrapper]_svg]:mr-3 [&_[cmdk-input-wrapper]_svg]:h-[18px] [&_[cmdk-input-wrapper]_svg]:w-[18px] [&_[cmdk-input]]:h-[52px] [&_[cmdk-input]]:text-[15px]"
          >
            <CommandInput
              placeholder={
                inPicker
                  ? "Choose a cluster (Backspace goes back)"
                  : "Search guests, actions and pages"
              }
              value={query}
              onValueChange={setQuery}
            />
            <CommandList className="max-h-[min(460px,60vh)] pb-2">
              {inPicker ? (
                <CommandGroup
                  heading={
                    view === "create-vm"
                      ? "Create a VM: choose a cluster"
                      : view === "create-import"
                        ? "Import a VM: choose a cluster"
                        : "Create a container: choose a cluster"
                  }
                >
                  {(clusters ?? [])
                    .filter(
                      (c) => q.length === 0 || c.name.toLowerCase().includes(q),
                    )
                    .map((c) => (
                      <CommandItem
                        key={c.id}
                        className={ROW_CLASS}
                        onSelect={() => {
                          pickCreateCluster(c.id);
                        }}
                      >
                        <StatusIcon status={c.status} />
                        <span className="min-w-0 truncate">{c.name}</span>
                        {c.pve_version !== "" && (
                          <RowAside>PVE {c.pve_version}</RowAside>
                        )}
                      </CommandItem>
                    ))}
                </CommandGroup>
              ) : (
                <>
                  {!hasResults && (
                    <CommandEmpty className="px-[18px] py-8 text-center text-sm text-muted-foreground">
                      {searchQuery.isLoading
                        ? "Searching…"
                        : "No results found."}
                    </CommandEmpty>
                  )}
                  {actionMatches.length > 0 && (
                    <CommandGroup heading="Actions">
                      {actionMatches.map((a) => (
                        <CommandItem
                          key={a.id}
                          className={ROW_CLASS}
                          onSelect={a.perform}
                        >
                          <RowIcon>{a.icon}</RowIcon>
                          <span className="min-w-0 truncate">{a.label}</span>
                          <RowAside>{a.description}</RowAside>
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  )}
                  {grouped.vms.length > 0 && (
                    <CommandGroup heading="Guests">
                      {grouped.vms.map((r) => (
                        <VMContextMenu
                          key={`${r.cluster_id}-${r.id}`}
                          target={{
                            clusterId: r.cluster_id,
                            resourceId: r.id,
                            vmid: r.vmid ?? 0,
                            name: r.name,
                            kind: r.type === "ct" ? "ct" : "vm",
                            status: r.status ?? "unknown",
                            currentNode: r.node ?? "",
                            template: r.template ?? false,
                          }}
                          onAction={close}
                        >
                          <CommandItem
                            className={ROW_CLASS}
                            onSelect={() => {
                              handleSelect(r);
                            }}
                          >
                            <StatusIcon status={r.status ?? "unknown"} />
                            <RowIcon>{getIcon(r.type)}</RowIcon>
                            <span className="min-w-0 truncate">{r.name}</span>
                            <RowAside>
                              {location(guestKind(r), r.cluster_name, r.node)}
                            </RowAside>
                          </CommandItem>
                        </VMContextMenu>
                      ))}
                    </CommandGroup>
                  )}
                  {grouped.nodes.length > 0 && (
                    <CommandGroup heading="Nodes">
                      {grouped.nodes.map((r) => (
                        <CommandItem
                          key={`${r.cluster_id}-${r.id}`}
                          className={ROW_CLASS}
                          onSelect={() => {
                            handleSelect(r);
                          }}
                        >
                          <StatusIcon status={r.status ?? "unknown"} />
                          <RowIcon>{getIcon(r.type)}</RowIcon>
                          <span className="min-w-0 truncate">{r.name}</span>
                          <RowAside>
                            {location("Node", r.cluster_name)}
                          </RowAside>
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  )}
                  {grouped.storage.length > 0 && (
                    <CommandGroup heading="Storage">
                      {grouped.storage.map((r) => (
                        <CommandItem
                          key={`${r.cluster_id}-${r.id}`}
                          className={ROW_CLASS}
                          onSelect={() => {
                            handleSelect(r);
                          }}
                        >
                          <RowIcon>{getIcon(r.type)}</RowIcon>
                          <span className="min-w-0 truncate">{r.name}</span>
                          <RowAside>
                            {location("Storage", r.cluster_name, r.node)}
                          </RowAside>
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  )}
                  {grouped.ups.length > 0 && (
                    <CommandGroup heading="UPS">
                      {grouped.ups.map((r) => (
                        <CommandItem
                          key={`ups-${r.id}`}
                          className={ROW_CLASS}
                          onSelect={() => {
                            handleSelect(r);
                          }}
                        >
                          <RowIcon>{getIcon(r.type)}</RowIcon>
                          <span className="min-w-0 truncate">{r.name}</span>
                          <RowAside>{location("UPS", r.server)}</RowAside>
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  )}
                  {(grouped.clusters.length > 0 ||
                    clusterMatches.length > 0) && (
                    <CommandGroup heading="Clusters">
                      {grouped.clusters.map((r) => (
                        <CommandItem
                          key={`cluster-${r.cluster_id}`}
                          className={ROW_CLASS}
                          onSelect={() => {
                            handleSelect(r);
                          }}
                        >
                          <StatusIcon status={r.status ?? "unknown"} />
                          <RowIcon>{getIcon(r.type)}</RowIcon>
                          <span className="min-w-0 truncate">{r.name}</span>
                          <RowAside>Cluster</RowAside>
                        </CommandItem>
                      ))}
                      {clusterMatches.map((c) => (
                        <CommandItem
                          key={`cl-${c.id}`}
                          className={ROW_CLASS}
                          onSelect={() => {
                            goTo(`/clusters/${c.id}`);
                          }}
                        >
                          <StatusIcon status={c.status} />
                          <RowIcon>
                            <Layers className="h-4 w-4" />
                          </RowIcon>
                          <span className="min-w-0 truncate">{c.name}</span>
                          <RowAside>
                            {location(
                              "Cluster",
                              c.pve_version !== ""
                                ? `PVE ${c.pve_version}`
                                : undefined,
                            )}
                          </RowAside>
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  )}
                  {q.length < 2 && pinnedPages.length > 0 && (
                    <CommandGroup heading="Go to">
                      {pinnedPages.map((p) => (
                        <CommandItem
                          key={p.path}
                          className={ROW_CLASS}
                          onSelect={() => {
                            goTo(p.path ?? "/");
                          }}
                        >
                          <RowIcon>{p.icon}</RowIcon>
                          <span className="min-w-0 truncate">{p.label}</span>
                          <RowAside>{p.description}</RowAside>
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  )}
                  {pageMatches.length > 0 && (
                    <CommandGroup heading="Pages and settings">
                      {pageMatches.map((p) => (
                        <CommandItem
                          key={p.path}
                          className={ROW_CLASS}
                          onSelect={() => {
                            goTo(p.path);
                          }}
                        >
                          <RowIcon>{p.icon}</RowIcon>
                          <span className="min-w-0 truncate">{p.label}</span>
                          <RowAside>
                            {location(p.description, p.clusterName)}
                          </RowAside>
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  )}
                </>
              )}
            </CommandList>
            <div className="hidden items-center gap-4 border-t px-[18px] py-2.5 text-[12.5px] text-muted-foreground sm:flex">
              <span className="flex items-center gap-1.5">
                <Kbd>↑</Kbd>
                <Kbd>↓</Kbd> navigate
              </span>
              <span className="flex items-center gap-1.5">
                <Kbd>↵</Kbd> select
              </span>
              {inPicker && (
                <span className="flex items-center gap-1.5">
                  <Kbd>⌫</Kbd> back
                </span>
              )}
              {!inPicker && grouped.vms.length > 0 && (
                <span>Right-click a guest for its actions</span>
              )}
              <span className="ml-auto flex items-center gap-1.5">
                <Kbd>esc</Kbd> close
              </span>
            </div>
          </Command>
        </DialogContent>
      </Dialog>
    </>
  );
}
