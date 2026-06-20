import { useState, useEffect, useCallback, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList,
} from "@/components/ui/command";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useGlobalSearch, type SearchResult } from "@/features/search/api/search-queries";
import { VMContextMenu } from "@/features/vms/components/VMContextMenu";
import { useAuth } from "@/hooks/useAuth";
import { useThemeStore } from "@/stores/theme-store";
import { StatusIcon } from "@/components/StatusIcon";
import { CreateVMDialog } from "@/features/vms/components/CreateVMDialog";
import { CreateCTDialog } from "@/features/vms/components/CreateCTDialog";
import {
  Monitor, Server, HardDrive, Database, Search, Layers,
  Settings, Shield, Network, Repeat, Award, BarChart3,
  Bell, FileText, Map, Eye, Users, Key, Lock, Palette,
  Tag, Cpu, Globe, Container, TerminalSquare, Sun, Moon,
  MonitorCog, Plus,
} from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

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
  /** Show in the zero-query "Go to" group */
  pinned?: boolean;
}

const GLOBAL_PAGES: PageEntry[] = [
  { keywords: ["dashboard", "home", "overview"], label: "Dashboard", description: "Main dashboard", icon: <BarChart3 className="h-4 w-4" />, path: "/", pinned: true },
  { keywords: ["inventory", "vms", "containers", "virtual machines"], label: "Inventory", description: "All VMs & containers", icon: <Monitor className="h-4 w-4" />, path: "/inventory", pinned: true },
  { keywords: ["storage", "disk", "volumes"], label: "Storage", description: "Storage pools", icon: <HardDrive className="h-4 w-4" />, path: "/storage", pinned: true },
  { keywords: ["backup", "restore", "pbs"], label: "Backup", description: "Backup dashboard", icon: <FileText className="h-4 w-4" />, path: "/backup", pinned: true },
  { keywords: ["topology", "map", "infrastructure", "diagram"], label: "Topology", description: "Infrastructure map", icon: <Map className="h-4 w-4" />, path: "/topology", pinned: true },
  { keywords: ["alerts", "notifications", "rules", "channels"], label: "Alerts", description: "Alert rules & history", icon: <Bell className="h-4 w-4" />, path: "/alerts", requiredPermission: "view:alert", pinned: true },
  { keywords: ["reports", "schedule", "generate"], label: "Reports", description: "Report schedules", icon: <FileText className="h-4 w-4" />, path: "/reports", requiredPermission: "view:report" },
  { keywords: ["events", "audit", "log", "syslog"], label: "Events", description: "Event log", icon: <Eye className="h-4 w-4" />, path: "/events", requiredPermission: "view:audit", pinned: true },
  { keywords: ["security", "cve", "vulnerability", "scanning", "rolling", "update"], label: "Security", description: "CVE scanning & rolling updates", icon: <Shield className="h-4 w-4" />, path: "/security", requiredPermission: "view:cve_scan", pinned: true },
  { keywords: ["users", "admin", "accounts", "rbac"], label: "Admin: Users", description: "User management", icon: <Users className="h-4 w-4" />, path: "/admin/users", requiredPermission: "manage:user" },
  { keywords: ["roles", "permissions", "rbac", "admin"], label: "Admin: Roles", description: "Role management", icon: <Key className="h-4 w-4" />, path: "/admin/roles", requiredPermission: "manage:user" },
  { keywords: ["ldap", "active directory", "ad", "admin"], label: "Admin: LDAP", description: "LDAP/AD configuration", icon: <Globe className="h-4 w-4" />, path: "/admin/ldap", requiredPermission: "manage:user" },
  { keywords: ["oidc", "sso", "oauth", "admin"], label: "Admin: OIDC/SSO", description: "OIDC provider configuration", icon: <Lock className="h-4 w-4" />, path: "/admin/oidc", requiredPermission: "manage:user" },
  { keywords: ["branding", "logo", "title", "admin"], label: "Admin: Branding", description: "App branding & logo", icon: <Palette className="h-4 w-4" />, path: "/admin/branding", requiredPermission: "manage:user" },
  { keywords: ["appearance", "theme", "dark", "light", "accent", "color"], label: "Settings: Appearance", description: "Theme & display preferences", icon: <Palette className="h-4 w-4" />, path: "/settings/appearance" },
  { keywords: ["security", "totp", "2fa", "two factor", "mfa"], label: "Settings: Security", description: "Two-factor authentication", icon: <Shield className="h-4 w-4" />, path: "/settings/security" },
];

/** Per-cluster pages — one entry generated per cluster */
const CLUSTER_PAGES: PageEntry[] = [
  { keywords: ["drs", "scheduler", "resource", "balancing", "affinity"], label: "DRS", description: "Dynamic resource scheduler", icon: <Cpu className="h-4 w-4" />, clusterPath: "/clusters/{id}?tab=drs" },
  { keywords: ["firewall", "rules", "aliases", "ipset", "security group"], label: "Firewall", description: "Cluster firewall rules", icon: <Shield className="h-4 w-4" />, clusterPath: "/clusters/{id}?tab=firewall" },
  { keywords: ["ceph", "osd", "pool", "monitor", "rados"], label: "Ceph", description: "Ceph storage cluster", icon: <Database className="h-4 w-4" />, clusterPath: "/clusters/{id}?tab=ceph" },
  { keywords: ["network", "vnet", "sdn", "bridge", "vlan"], label: "Networks", description: "Cluster networking", icon: <Network className="h-4 w-4" />, clusterPath: "/clusters/{id}?tab=networks" },
  { keywords: ["options", "datacenter", "notes", "description", "settings", "config"], label: "Options", description: "Datacenter options & notes", icon: <Settings className="h-4 w-4" />, clusterPath: "/clusters/{id}?tab=options" },
  { keywords: ["ha", "high availability", "failover", "fencing"], label: "HA", description: "High availability", icon: <Repeat className="h-4 w-4" />, clusterPath: "/clusters/{id}?tab=ha" },
  { keywords: ["pool", "resource pool"], label: "Pools", description: "Resource pools", icon: <Layers className="h-4 w-4" />, clusterPath: "/clusters/{id}?tab=pools" },
  { keywords: ["replication", "zfs", "sync", "replicate"], label: "Replication", description: "ZFS replication jobs", icon: <Repeat className="h-4 w-4" />, clusterPath: "/clusters/{id}?tab=replication" },
  { keywords: ["certificate", "acme", "letsencrypt", "ssl", "tls"], label: "Certificates", description: "ACME certificates", icon: <Award className="h-4 w-4" />, clusterPath: "/clusters/{id}?tab=certificates" },
  { keywords: ["metric", "influxdb", "graphite", "metrics server"], label: "Metric Servers", description: "External metric targets", icon: <BarChart3 className="h-4 w-4" />, clusterPath: "/clusters/{id}?tab=metric-servers" },
  { keywords: ["tags", "tag", "label", "registered"], label: "Tags", description: "Tag management", icon: <Tag className="h-4 w-4" />, clusterPath: "/clusters/{id}?tab=options" },
];

interface PageMatch {
  label: string;
  description: string;
  icon: ReactNode;
  path: string;
  clusterName?: string;
}

function IconChip({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <span
      className={cn(
        "mr-2 flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground",
        className,
      )}
    >
      {children}
    </span>
  );
}

function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="rounded border bg-muted px-1.5 font-mono text-[10px] font-medium text-muted-foreground">
      {children}
    </kbd>
  );
}

type PaletteView = "root" | "create-vm" | "create-ct";

export function SearchBar() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [view, setView] = useState<PaletteView>("root");
  const [createVMOpen, setCreateVMOpen] = useState(false);
  const [createCTOpen, setCreateCTOpen] = useState(false);
  const [createCluster, setCreateCluster] = useState("");
  const navigate = useNavigate();
  const { hasPermission } = useAuth();
  const setThemeMode = useThemeStore((s) => s.setMode);
  const searchQuery = useGlobalSearch(query);
  const { data: clusters } = useClusters();

  const can = useCallback(
    (perm?: string) => {
      if (!perm) return true;
      const parts = perm.split(":");
      return hasPermission(parts[0] ?? "", parts[1] ?? "");
    },
    [hasPermission],
  );

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setOpen((prev) => !prev);
      }
    };
    document.addEventListener("keydown", handler);
    return () => { document.removeEventListener("keydown", handler); };
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
    (type: "vm" | "ct") => {
      const list = clusters ?? [];
      if (list.length === 1 && list[0]) {
        setCreateCluster(list[0].id);
        setOpen(false);
        setQuery("");
        setView("root");
        if (type === "vm") setCreateVMOpen(true);
        else setCreateCTOpen(true);
        return;
      }
      setQuery("");
      setView(type === "vm" ? "create-vm" : "create-ct");
    },
    [clusters],
  );

  const pickCreateCluster = useCallback(
    (clusterId: string) => {
      setCreateCluster(clusterId);
      const type = view;
      setOpen(false);
      setQuery("");
      setView("root");
      if (type === "create-vm") setCreateVMOpen(true);
      else setCreateCTOpen(true);
    },
    [view],
  );

  interface ActionEntry {
    id: string;
    label: string;
    description: string;
    keywords: string[];
    icon: ReactNode;
    chipClass: string;
    perform: () => void;
  }

  const actions = useMemo<ActionEntry[]>(
    () => [
      { id: "create-vm", label: "Create virtual machine…", description: "New QEMU guest", keywords: ["create", "new", "vm", "virtual machine", "qemu"], icon: <Plus className="h-4 w-4" />, chipClass: "bg-emerald-500/10 text-emerald-500", perform: () => { startCreate("vm"); } },
      { id: "create-ct", label: "Create container…", description: "New LXC guest", keywords: ["create", "new", "ct", "container", "lxc"], icon: <Container className="h-4 w-4" />, chipClass: "bg-sky-500/10 text-sky-500", perform: () => { startCreate("ct"); } },
      { id: "console", label: "Open console", description: "Terminal & VNC sessions", keywords: ["console", "terminal", "shell", "vnc", "xterm"], icon: <TerminalSquare className="h-4 w-4" />, chipClass: "bg-violet-500/10 text-violet-500", perform: () => { goTo("/console"); } },
      { id: "theme-dark", label: "Theme: dark", description: "Switch to dark mode", keywords: ["theme", "dark", "mode", "appearance"], icon: <Moon className="h-4 w-4" />, chipClass: "bg-amber-500/10 text-amber-500", perform: () => { setThemeMode("dark"); close(); } },
      { id: "theme-light", label: "Theme: light", description: "Switch to light mode", keywords: ["theme", "light", "mode", "appearance"], icon: <Sun className="h-4 w-4" />, chipClass: "bg-amber-500/10 text-amber-500", perform: () => { setThemeMode("light"); close(); } },
      { id: "theme-system", label: "Theme: system", description: "Follow the OS preference", keywords: ["theme", "system", "auto", "mode", "appearance"], icon: <MonitorCog className="h-4 w-4" />, chipClass: "bg-amber-500/10 text-amber-500", perform: () => { setThemeMode("system"); close(); } },
    ],
    [startCreate, goTo, setThemeMode, close],
  );

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
      if (!can(page.requiredPermission)) continue;
      if (page.keywords.some((kw) => kw.includes(q)) || page.label.toLowerCase().includes(q)) {
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
      if (page.keywords.some((kw) => kw.includes(q)) || page.label.toLowerCase().includes(q)) {
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
    () => GLOBAL_PAGES.filter((p) => p.pinned && can(p.requiredPermission)),
    [can],
  );

  const clusterMatches = useMemo(() => {
    const list = clusters ?? [];
    if (q.length >= 2) return [];
    return list.filter((c) => q.length === 0 || c.name.toLowerCase().includes(q));
  }, [clusters, q]);

  const handleSelect = useCallback((result: SearchResult) => {
    close();
    switch (result.type) {
      case "vm":
      case "ct":
        void navigate(`/inventory/${result.type}/${result.cluster_id}/${result.id}`);
        break;
      case "node":
        void navigate(`/clusters/${result.cluster_id}/nodes/${result.id}`);
        break;
      case "storage":
        void navigate(`/storage?cluster=${result.cluster_id}`);
        break;
      default:
        void navigate(`/clusters/${result.cluster_id}`);
        break;
    }
  }, [close, navigate]);

  const getIcon = (type: string) => {
    switch (type) {
      case "vm": return <Monitor className="h-4 w-4" />;
      case "ct": return <Database className="h-4 w-4" />;
      case "node": return <Server className="h-4 w-4" />;
      case "storage": return <HardDrive className="h-4 w-4" />;
      case "cluster": return <Layers className="h-4 w-4" />;
      default: return null;
    }
  };

  const results = searchQuery.data ?? [];
  const grouped = {
    vms: results.filter((r) => r.type === "vm" || r.type === "ct"),
    nodes: results.filter((r) => r.type === "node"),
    storage: results.filter((r) => r.type === "storage"),
    clusters: results.filter((r) => r.type === "cluster"),
  };

  const hasResults =
    grouped.vms.length > 0 || grouped.nodes.length > 0 || grouped.storage.length > 0 ||
    grouped.clusters.length > 0 || pageMatches.length > 0 || actionMatches.length > 0 ||
    clusterMatches.length > 0 || q.length < 2;

  const inPicker = view !== "root";

  return (
    <>
      <button
        onClick={() => { setOpen(true); }}
        className="flex h-9 w-full max-w-64 items-center gap-2 rounded-md border border-input bg-background px-3 text-sm text-muted-foreground shadow-xs transition-colors hover:bg-accent hover:text-accent-foreground"
      >
        <Search className="h-4 w-4" />
        <span className="flex-1 text-left">Search or jump to…</span>
        <kbd className="pointer-events-none hidden h-5 select-none items-center gap-1 rounded border bg-muted px-1.5 font-mono text-[10px] font-medium opacity-100 sm:flex">
          Ctrl+K
        </kbd>
      </button>

      <Dialog open={open} onOpenChange={handleOpenChange}>
        <DialogContent className="top-[16%] max-w-[calc(100%-1.5rem)] translate-y-0 overflow-hidden p-0 sm:max-w-[560px]">
          <DialogTitle className="sr-only">Command palette</DialogTitle>
          <Command
            shouldFilter={false}
            onKeyDown={(e) => {
              if (inPicker && e.key === "Backspace" && query === "") {
                e.preventDefault();
                setView("root");
              }
            }}
            className="[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-muted-foreground [&_[cmdk-group]]:px-2 [&_[cmdk-group]:not([hidden])_~[cmdk-group]]:pt-0 [&_[cmdk-input-wrapper]_svg]:h-5 [&_[cmdk-input-wrapper]_svg]:w-5 [&_[cmdk-input]]:h-12 [&_[cmdk-item]]:px-2 [&_[cmdk-item]]:py-2"
          >
            <CommandInput
              placeholder={
                inPicker
                  ? "Choose a cluster… (backspace to go back)"
                  : "Search guests, run an action, jump anywhere…"
              }
              value={query}
              onValueChange={setQuery}
            />
            <CommandList className="max-h-[420px]">
              {inPicker ? (
                <CommandGroup heading={view === "create-vm" ? "Create VM — choose cluster" : "Create CT — choose cluster"}>
                  {(clusters ?? [])
                    .filter((c) => q.length === 0 || c.name.toLowerCase().includes(q))
                    .map((c) => (
                      <CommandItem key={c.id} onSelect={() => { pickCreateCluster(c.id); }}>
                        <StatusIcon status={c.status} className="mr-2" />
                        <span className="flex-1">{c.name}</span>
                        {c.pve_version !== "" && (
                          <span className="text-xs text-muted-foreground">PVE {c.pve_version}</span>
                        )}
                      </CommandItem>
                    ))}
                </CommandGroup>
              ) : (
                <>
                  {!hasResults && (
                    <CommandEmpty>
                      {searchQuery.isLoading ? "Searching…" : "No results found."}
                    </CommandEmpty>
                  )}
                  {actionMatches.length > 0 && (
                    <CommandGroup heading="Actions">
                      {actionMatches.map((a) => (
                        <CommandItem key={a.id} onSelect={a.perform}>
                          <IconChip className={a.chipClass}>{a.icon}</IconChip>
                          <span className="flex-1">
                            {a.label}
                            <span className="ml-2 text-xs text-muted-foreground">{a.description}</span>
                          </span>
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  )}
                  {grouped.vms.length > 0 && (
                    <CommandGroup heading="VMs / Containers">
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
                          <CommandItem onSelect={() => { handleSelect(r); }}>
                            <StatusIcon status={r.status ?? "unknown"} className="mr-2" />
                            <IconChip>{getIcon(r.type)}</IconChip>
                            <span className="flex-1">{r.name}{r.vmid ? ` (${String(r.vmid)})` : ""}</span>
                            <span className="text-xs text-muted-foreground">{r.cluster_name}{r.node ? ` / ${r.node}` : ""}</span>
                          </CommandItem>
                        </VMContextMenu>
                      ))}
                    </CommandGroup>
                  )}
                  {grouped.nodes.length > 0 && (
                    <CommandGroup heading="Nodes">
                      {grouped.nodes.map((r) => (
                        <CommandItem key={`${r.cluster_id}-${r.id}`} onSelect={() => { handleSelect(r); }}>
                          <StatusIcon status={r.status ?? "unknown"} className="mr-2" />
                          <IconChip>{getIcon(r.type)}</IconChip>
                          <span className="flex-1">{r.name}</span>
                          <span className="text-xs text-muted-foreground">{r.cluster_name}</span>
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  )}
                  {grouped.storage.length > 0 && (
                    <CommandGroup heading="Storage">
                      {grouped.storage.map((r) => (
                        <CommandItem key={`${r.cluster_id}-${r.id}`} onSelect={() => { handleSelect(r); }}>
                          <IconChip>{getIcon(r.type)}</IconChip>
                          <span className="flex-1">{r.name}</span>
                          <span className="text-xs text-muted-foreground">{r.cluster_name}{r.node ? ` / ${r.node}` : ""}</span>
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  )}
                  {(grouped.clusters.length > 0 || clusterMatches.length > 0) && (
                    <CommandGroup heading="Clusters">
                      {grouped.clusters.map((r) => (
                        <CommandItem key={`cluster-${r.cluster_id}`} onSelect={() => { handleSelect(r); }}>
                          <StatusIcon status={r.status ?? "unknown"} className="mr-2" />
                          <IconChip>{getIcon(r.type)}</IconChip>
                          <span className="flex-1">{r.name}</span>
                        </CommandItem>
                      ))}
                      {clusterMatches.map((c) => (
                        <CommandItem key={`cl-${c.id}`} onSelect={() => { goTo(`/clusters/${c.id}`); }}>
                          <StatusIcon status={c.status} className="mr-2" />
                          <IconChip><Layers className="h-4 w-4" /></IconChip>
                          <span className="flex-1">{c.name}</span>
                          {c.pve_version !== "" && (
                            <span className="text-xs text-muted-foreground">PVE {c.pve_version}</span>
                          )}
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  )}
                  {q.length < 2 && pinnedPages.length > 0 && (
                    <CommandGroup heading="Go to">
                      {pinnedPages.map((p) => (
                        <CommandItem key={p.path} onSelect={() => { goTo(p.path ?? "/"); }}>
                          <IconChip>{p.icon}</IconChip>
                          <span className="flex-1">
                            {p.label}
                            <span className="ml-2 text-xs text-muted-foreground">{p.description}</span>
                          </span>
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  )}
                  {pageMatches.length > 0 && (
                    <CommandGroup heading="Pages & Settings">
                      {pageMatches.map((p) => (
                        <CommandItem key={p.path} onSelect={() => { goTo(p.path); }}>
                          <IconChip>{p.icon}</IconChip>
                          <span className="flex-1">
                            {p.label}
                            <span className="ml-2 text-xs text-muted-foreground">{p.description}</span>
                          </span>
                          {p.clusterName && (
                            <span className="text-xs text-muted-foreground">{p.clusterName}</span>
                          )}
                        </CommandItem>
                      ))}
                    </CommandGroup>
                  )}
                </>
              )}
            </CommandList>
            <div className="hidden items-center gap-3 border-t px-3 py-2 text-[11px] text-muted-foreground sm:flex">
              <span className="flex items-center gap-1"><Kbd>↑</Kbd><Kbd>↓</Kbd> navigate</span>
              <span className="flex items-center gap-1"><Kbd>↵</Kbd> select</span>
              {inPicker && <span className="flex items-center gap-1"><Kbd>⌫</Kbd> back</span>}
              {!inPicker && grouped.vms.length > 0 && (
                <span>right-click a guest for actions</span>
              )}
              <span className="ml-auto flex items-center gap-1"><Kbd>esc</Kbd> close</span>
            </div>
          </Command>
        </DialogContent>
      </Dialog>

      {createVMOpen && (
        <CreateVMDialog
          open={createVMOpen}
          onOpenChange={setCreateVMOpen}
          clusterId={createCluster}
        />
      )}
      {createCTOpen && (
        <CreateCTDialog
          open={createCTOpen}
          onOpenChange={setCreateCTOpen}
          clusterId={createCluster}
        />
      )}
    </>
  );
}
