import { useState, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { Search, ChevronDown, ChevronRight, Loader2 } from "lucide-react";

import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel, PropertyList, PropertyRow } from "@/components/page/Panel";
import { useExpandedRows } from "@/hooks/useExpandedRows";
import { usePermissions } from "@/hooks/usePermissions";
import type { APIEndpoint } from "@/types/api";
import { useAPIDocs } from "../api/api-docs-queries";
import { APIEndpointRow } from "../components/APIEndpointRow";

/** Stable identity for one endpoint, for the expanded-row set. */
function endpointKey(ep: APIEndpoint): string {
  return `${ep.method} ${ep.path}`;
}

export function APIDocsPage() {
  const navigate = useNavigate();
  const { data: endpoints, isLoading } = useAPIDocs();
  const [search, setSearch] = useState("");
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(
    new Set(),
  );
  const { expanded, toggle } = useExpandedRows();
  // Creating a key takes manage:api_key install-wide (registry_api_keys.go).
  const canCreateKey = usePermissions().hasGlobalPermission(
    "manage",
    "api_key",
  );

  const filtered = useMemo(() => {
    if (!endpoints) return [];
    if (!search.trim()) return endpoints;
    const q = search.toLowerCase();
    return endpoints.filter(
      (ep) =>
        ep.path.toLowerCase().includes(q) ||
        ep.description.toLowerCase().includes(q) ||
        ep.group.toLowerCase().includes(q) ||
        // Parameter names too, or filtering HIDES the endpoint whose only
        // match is the parameter being searched for — which is the search
        // someone reaches this page to run.
        (ep.parameters ?? []).some((p) => p.name.toLowerCase().includes(q)),
    );
  }, [endpoints, search]);

  const grouped = useMemo(() => {
    const map = new Map<string, APIEndpoint[]>();
    for (const ep of filtered) {
      const group = ep.group || "Other";
      const existing = map.get(group);
      if (existing) {
        existing.push(ep);
      } else {
        map.set(group, [ep]);
      }
    }
    return map;
  }, [filtered]);

  const toggleGroup = (group: string) => {
    setCollapsedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(group)) {
        next.delete(group);
      } else {
        next.add(group);
      }
      return next;
    });
  };

  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  const total = endpoints?.length ?? 0;
  const groupCount = new Set((endpoints ?? []).map((ep) => ep.group || "Other"))
    .size;
  const filtering = search.trim() !== "";

  return (
    // max-w-6xl rather than a narrower column: an expanded row carries a
    // five-column parameter table, and at 4xl the description column was
    // narrower than the sentences it holds.
    <div className="max-w-6xl space-y-5">
      <PageHeader
        title="API reference"
        // The count and the path are separate pieces on purpose: a template
        // literal that interpolates beside an API path is what the apiPath
        // guard looks for, and this one only describes the prefix.
        summary={
          <>
            {`${String(total)} endpoint${total !== 1 ? "s" : ""} in ${String(groupCount)} group${groupCount !== 1 ? "s" : ""}`}
            , every one under <code className="font-mono">/api/v1</code>.
          </>
        }
        actions={
          canCreateKey ? (
            <Button
              variant="outline"
              onClick={() => {
                void navigate("/settings/api-keys");
              }}
            >
              Create an API key
            </Button>
          ) : undefined
        }
      />

      <Panel title="Authentication">
        <PropertyList>
          <PropertyRow label="Base URL">
            <code className="font-mono text-[13px]">
              {window.location.origin}/api/v1
            </code>
          </PropertyRow>
          <PropertyRow label="Header">
            <code className="font-mono text-[13px]">
              Authorization: Bearer nxra_your_api_key_here
            </code>
          </PropertyRow>
        </PropertyList>
        <p className="mt-3 text-[12.5px] text-muted-foreground">
          Include your API key or JWT token in the Authorization header of every
          request.
        </p>
      </Panel>

      <Panel
        title="Endpoints"
        aside={
          filtering
            ? `${String(filtered.length)} of ${String(total)} match`
            : undefined
        }
        actions={
          <div className="relative w-[min(26rem,calc(100vw-4.5rem))]">
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              placeholder="Filter endpoints by path, description, group or parameter"
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
              }}
              className="pl-9"
            />
          </div>
        }
      >
        <p className="max-w-[80ch] text-[12.5px] text-muted-foreground">
          An endpoint with a chevron publishes its parameters: open it to see
          them. A row without one publishes no parameter list, which is not a
          promise that it takes no input. A few endpoints predate the parameter
          schema, and file uploads read form fields it does not describe.
        </p>
        {grouped.size === 0 && (
          <p className="pb-4 pt-8 text-center text-sm text-muted-foreground">
            No endpoints match your search.
          </p>
        )}
      </Panel>

      {/* Endpoint catalog: one panel per group */}
      {grouped.size > 0 && (
        <div className="space-y-3">
          {Array.from(grouped.entries()).map(([group, eps]) => {
            const isCollapsed = collapsedGroups.has(group);
            return (
              <Panel
                key={group}
                flush
                bodyClassName={isCollapsed ? "pb-0" : "pb-1"}
                title={
                  <button
                    type="button"
                    aria-expanded={!isCollapsed}
                    className="-ml-1 flex items-center gap-1.5 rounded-md pr-1 text-left outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
                    onClick={() => {
                      toggleGroup(group);
                    }}
                  >
                    {isCollapsed ? (
                      <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />
                    ) : (
                      <ChevronDown className="h-4 w-4 shrink-0 text-muted-foreground" />
                    )}
                    {group}
                  </button>
                }
                aside={String(eps.length)}
              >
                {!isCollapsed && (
                  <div className="divide-y border-t">
                    {eps.map((ep) => {
                      const key = endpointKey(ep);
                      return (
                        <APIEndpointRow
                          key={key}
                          endpoint={ep}
                          expanded={expanded.has(key)}
                          onToggle={() => {
                            toggle(key);
                          }}
                        />
                      );
                    })}
                  </div>
                )}
              </Panel>
            );
          })}
        </div>
      )}
    </div>
  );
}
