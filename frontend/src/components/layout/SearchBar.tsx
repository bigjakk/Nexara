import { useState, useEffect, useCallback, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList,
} from "@/components/ui/command";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useGlobalSearch, type SearchResult } from "@/features/search/api/search-queries";
import {
  Monitor, Server, HardDrive, Database, Search, Layers,
  Settings, Shield, Network, Repeat, Award, BarChart3,
  Bell, FileText, Map, Eye, Users, Key, Lock, Palette,
  Tag, Cpu, Globe,
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
}

const GLOBAL_PAGES: PageEntry[] = [
  { keywords: ["dashboard", "home", "overview"], label: "Dashboard", description: "Main dashboard", icon: <BarChart3 className="mr-2 h-4 w-4" />, path: "/" },
  { keywords: ["inventory", "vms", "containers", "virtual machines"], label: "Inventory", description: "All VMs & containers", icon: <Monitor className="mr-2 h-4 w-4" />, path: "/inventory" },
  { keywords: ["storage", "disk", "volumes"], label: "Storage", description: "Storage pools", icon: <HardDrive className="mr-2 h-4 w-4" />, path: "/storage" },
  { keywords: ["backup", "restore", "pbs"], label: "Backup", description: "Backup dashboard", icon: <FileText className="mr-2 h-4 w-4" />, path: "/backup" },
  { keywords: ["topology", "map", "infrastructure", "diagram"], label: "Topology", description: "Infrastructure map", icon: <Map className="mr-2 h-4 w-4" />, path: "/topology" },
  { keywords: ["alerts", "notifications", "rules", "channels"], label: "Alerts", description: "Alert rules & history", icon: <Bell className="mr-2 h-4 w-4" />, path: "/alerts" },
  { keywords: ["reports", "schedule", "generate"], label: "Reports", description: "Report schedules", icon: <FileText className="mr-2 h-4 w-4" />, path: "/reports" },
  { keywords: ["events", "audit", "log", "syslog"], label: "Events", description: "Event log", icon: <Eye className="mr-2 h-4 w-4" />, path: "/events" },
  { keywords: ["security", "cve", "vulnerability", "scanning", "rolling", "update"], label: "Security", description: "CVE scanning & rolling updates", icon: <Shield className="mr-2 h-4 w-4" />, path: "/security" },
  { keywords: ["users", "admin", "accounts", "rbac"], label: "Admin: Users", description: "User management", icon: <Users className="mr-2 h-4 w-4" />, path: "/admin/users" },
  { keywords: ["roles", "permissions", "rbac", "admin"], label: "Admin: Roles", description: "Role management", icon: <Key className="mr-2 h-4 w-4" />, path: "/admin/roles" },
  { keywords: ["ldap", "active directory", "ad", "admin"], label: "Admin: LDAP", description: "LDAP/AD configuration", icon: <Globe className="mr-2 h-4 w-4" />, path: "/admin/ldap" },
  { keywords: ["oidc", "sso", "oauth", "admin"], label: "Admin: OIDC/SSO", description: "OIDC provider configuration", icon: <Lock className="mr-2 h-4 w-4" />, path: "/admin/oidc" },
  { keywords: ["branding", "logo", "title", "admin"], label: "Admin: Branding", description: "App branding & logo", icon: <Palette className="mr-2 h-4 w-4" />, path: "/admin/branding" },
  { keywords: ["appearance", "theme", "dark", "light", "accent", "color"], label: "Settings: Appearance", description: "Theme & display preferences", icon: <Palette className="mr-2 h-4 w-4" />, path: "/settings/appearance" },
  { keywords: ["security", "totp", "2fa", "two factor", "mfa"], label: "Settings: Security", description: "Two-factor authentication", icon: <Shield className="mr-2 h-4 w-4" />, path: "/settings/security" },
];

/** Per-cluster pages — one entry generated per cluster */
const CLUSTER_PAGES: PageEntry[] = [
  { keywords: ["drs", "scheduler", "resource", "balancing", "affinity"], label: "DRS", description: "Dynamic resource scheduler", icon: <Cpu className="mr-2 h-4 w-4" />, clusterPath: "/clusters/{id}?tab=drs" },
  { keywords: ["firewall", "rules", "aliases", "ipset", "security group"], label: "Firewall", description: "Cluster firewall rules", icon: <Shield className="mr-2 h-4 w-4" />, clusterPath: "/clusters/{id}?tab=firewall" },
  { keywords: ["ceph", "osd", "pool", "monitor", "rados"], label: "Ceph", description: "Ceph storage cluster", icon: <Database className="mr-2 h-4 w-4" />, clusterPath: "/clusters/{id}?tab=ceph" },
  { keywords: ["network", "vnet", "sdn", "bridge", "vlan"], label: "Networks", description: "Cluster networking", icon: <Network className="mr-2 h-4 w-4" />, clusterPath: "/clusters/{id}?tab=networks" },
  { keywords: ["options", "datacenter", "notes", "description", "settings", "config"], label: "Options", description: "Datacenter options & notes", icon: <Settings className="mr-2 h-4 w-4" />, clusterPath: "/clusters/{id}?tab=options" },
  { keywords: ["ha", "high availability", "failover", "fencing"], label: "HA", description: "High availability", icon: <Repeat className="mr-2 h-4 w-4" />, clusterPath: "/clusters/{id}?tab=ha" },
  { keywords: ["pool", "resource pool"], label: "Pools", description: "Resource pools", icon: <Layers className="mr-2 h-4 w-4" />, clusterPath: "/clusters/{id}?tab=pools" },
  { keywords: ["replication", "zfs", "sync", "replicate"], label: "Replication", description: "ZFS replication jobs", icon: <Repeat className="mr-2 h-4 w-4" />, clusterPath: "/clusters/{id}?tab=replication" },
  { keywords: ["certificate", "acme", "letsencrypt", "ssl", "tls"], label: "Certificates", description: "ACME certificates", icon: <Award className="mr-2 h-4 w-4" />, clusterPath: "/clusters/{id}?tab=certificates" },
  { keywords: ["metric", "influxdb", "graphite", "metrics server"], label: "Metric Servers", description: "External metric targets", icon: <BarChart3 className="mr-2 h-4 w-4" />, clusterPath: "/clusters/{id}?tab=metric-servers" },
  { keywords: ["tags", "tag", "label", "registered"], label: "Tags", description: "Tag management", icon: <Tag className="mr-2 h-4 w-4" />, clusterPath: "/clusters/{id}?tab=options" },
];

interface PageMatch {
  label: string;
  description: string;
  icon: ReactNode;
  path: string;
  clusterName?: string;
}

export function SearchBar() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const navigate = useNavigate();
  const searchQuery = useGlobalSearch(query);
  const { data: clusters } = useClusters();

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

  // Filter static pages by query
  const pageMatches = useMemo(() => {
    if (query.length < 2) return [];
    const q = query.toLowerCase();
    const matches: PageMatch[] = [];

    // Global pages
    for (const page of GLOBAL_PAGES) {
      if (page.keywords.some((kw) => kw.includes(q)) || page.label.toLowerCase().includes(q)) {
        matches.push({
          label: page.label,
          description: page.description,
          icon: page.icon,
          path: page.path ?? "/",
        });
      }
    }

    // Per-cluster pages
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
  }, [query, clusters]);

  const handleSelect = useCallback((result: SearchResult) => {
    setOpen(false);
    setQuery("");
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
      case "cluster":
        void navigate(`/clusters/${result.cluster_id}`);
        break;
      default:
        void navigate(`/clusters/${result.cluster_id}`);
        break;
    }
  }, [navigate]);

  const handlePageSelect = useCallback((path: string) => {
    setOpen(false);
    setQuery("");
    void navigate(path);
  }, [navigate]);

  const getIcon = (type: string) => {
    switch (type) {
      case "vm": return <Monitor className="mr-2 h-4 w-4" />;
      case "ct": return <Database className="mr-2 h-4 w-4" />;
      case "node": return <Server className="mr-2 h-4 w-4" />;
      case "storage": return <HardDrive className="mr-2 h-4 w-4" />;
      case "cluster": return <Layers className="mr-2 h-4 w-4" />;
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

  const hasResults = grouped.vms.length > 0 || grouped.nodes.length > 0 || grouped.storage.length > 0 || grouped.clusters.length > 0 || pageMatches.length > 0;

  return (
    <>
      <button
        onClick={() => { setOpen(true); }}
        className="flex h-9 w-64 items-center gap-2 rounded-md border border-input bg-background px-3 text-sm text-muted-foreground shadow-sm transition-colors hover:bg-accent hover:text-accent-foreground"
      >
        <Search className="h-4 w-4" />
        <span className="flex-1 text-left">Search...</span>
        <kbd className="pointer-events-none hidden h-5 select-none items-center gap-1 rounded border bg-muted px-1.5 font-mono text-[10px] font-medium opacity-100 sm:flex">
          Ctrl+K
        </kbd>
      </button>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="overflow-hidden p-0">
          <DialogTitle className="sr-only">Search</DialogTitle>
          <Command shouldFilter={false} className="[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-muted-foreground [&_[cmdk-group]]:px-2 [&_[cmdk-group]:not([hidden])_~[cmdk-group]]:pt-0 [&_[cmdk-input-wrapper]_svg]:h-5 [&_[cmdk-input-wrapper]_svg]:w-5 [&_[cmdk-input]]:h-12 [&_[cmdk-item]]:px-2 [&_[cmdk-item]]:py-3 [&_[cmdk-item]_svg]:h-5 [&_[cmdk-item]_svg]:w-5">
            <CommandInput placeholder="Search VMs, nodes, storage, settings..." value={query} onValueChange={setQuery} />
            <CommandList>
              {!hasResults && (
                <CommandEmpty>
                  {query.length < 2
                    ? "Type at least 2 characters..."
                    : searchQuery.isLoading
                      ? "Searching..."
                      : "No results found."}
                </CommandEmpty>
              )}
              {grouped.vms.length > 0 && (
                <CommandGroup heading="VMs / Containers">
                  {grouped.vms.map((r) => (
                    <CommandItem key={`${r.cluster_id}-${r.id}`} onSelect={() => { handleSelect(r); }}>
                      {getIcon(r.type)}
                      <span className="flex-1">{r.name}{r.vmid ? ` (${String(r.vmid)})` : ""}</span>
                      <span className="text-xs text-muted-foreground">{r.cluster_name} / {r.node}</span>
                    </CommandItem>
                  ))}
                </CommandGroup>
              )}
              {grouped.nodes.length > 0 && (
                <CommandGroup heading="Nodes">
                  {grouped.nodes.map((r) => (
                    <CommandItem key={`${r.cluster_id}-${r.id}`} onSelect={() => { handleSelect(r); }}>
                      {getIcon(r.type)}
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
                      {getIcon(r.type)}
                      <span className="flex-1">{r.name}</span>
                      <span className="text-xs text-muted-foreground">{r.cluster_name}{r.node ? ` / ${r.node}` : ""}</span>
                    </CommandItem>
                  ))}
                </CommandGroup>
              )}
              {grouped.clusters.length > 0 && (
                <CommandGroup heading="Clusters">
                  {grouped.clusters.map((r) => (
                    <CommandItem key={`cluster-${r.cluster_id}`} onSelect={() => { handleSelect(r); }}>
                      {getIcon(r.type)}
                      <span className="flex-1">{r.name}</span>
                    </CommandItem>
                  ))}
                </CommandGroup>
              )}
              {pageMatches.length > 0 && (
                <CommandGroup heading="Pages & Settings">
                  {pageMatches.map((p) => (
                    <CommandItem key={p.path} onSelect={() => { handlePageSelect(p.path); }}>
                      {p.icon}
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
            </CommandList>
          </Command>
        </DialogContent>
      </Dialog>
    </>
  );
}
