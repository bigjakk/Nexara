import { useState, useMemo } from "react";
import { useNavigate } from "react-router-dom";
import {
  Search,
  ChevronDown,
  ChevronRight,
  Key,
  ArrowLeft,
  Loader2,
} from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { APIEndpoint } from "@/types/api";
import { useAPIDocs } from "../api/api-docs-queries";

const METHOD_COLORS: Record<string, string> = {
  GET: "text-emerald-600 border-emerald-300 dark:text-emerald-400 dark:border-emerald-700",
  POST: "text-blue-600 border-blue-300 dark:text-blue-400 dark:border-blue-700",
  PUT: "text-amber-600 border-amber-300 dark:text-amber-400 dark:border-amber-700",
  DELETE: "text-red-600 border-red-300 dark:text-red-400 dark:border-red-700",
  PATCH: "text-purple-600 border-purple-300 dark:text-purple-400 dark:border-purple-700",
};

function getMethodColor(method: string): string {
  return METHOD_COLORS[method.toUpperCase()] ?? "text-muted-foreground";
}

export function APIDocsPage() {
  const navigate = useNavigate();
  const { data: endpoints, isLoading } = useAPIDocs();
  const [search, setSearch] = useState("");
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(
    new Set(),
  );

  const filtered = useMemo(() => {
    if (!endpoints) return [];
    if (!search.trim()) return endpoints;
    const q = search.toLowerCase();
    return endpoints.filter(
      (ep) =>
        ep.path.toLowerCase().includes(q) ||
        ep.description.toLowerCase().includes(q) ||
        ep.group.toLowerCase().includes(q),
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

  return (
    <div className="mx-auto max-w-4xl space-y-6 p-6">
      <div>
        <h1 className="text-2xl font-bold">API Reference</h1>
        <p className="text-muted-foreground">
          Complete reference of all available Nexara REST API endpoints
        </p>
      </div>

      {/* Auth info card */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Key className="h-5 w-5" />
            Authentication
          </CardTitle>
          <CardDescription>
            How to authenticate with the Nexara API
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-2">
            <div className="flex items-baseline gap-2">
              <span className="text-sm font-medium">Base URL:</span>
              <code className="rounded bg-muted px-2 py-1 font-mono text-sm">
                {window.location.origin}/api/v1
              </code>
            </div>
            <div>
              <span className="text-sm font-medium">Authentication:</span>
              <p className="text-sm text-muted-foreground">
                Include your API key or JWT token in the Authorization header
              </p>
            </div>
            <div>
              <span className="text-sm font-medium">Example:</span>
              <code className="mt-1 block rounded bg-muted px-3 py-2 font-mono text-sm">
                Authorization: Bearer nxra_your_api_key_here
              </code>
            </div>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              void navigate("/settings/api-keys");
            }}
          >
            <ArrowLeft className="mr-2 h-4 w-4" />
            Create an API key
          </Button>
        </CardContent>
      </Card>

      {/* Search/filter */}
      <div className="relative">
        <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          placeholder="Filter endpoints by path, description, or group..."
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
          }}
          className="pl-10"
        />
      </div>

      {/* Endpoint catalog */}
      {grouped.size === 0 ? (
        <div className="py-12 text-center text-muted-foreground">
          No endpoints match your search.
        </div>
      ) : (
        <div className="space-y-3">
          {Array.from(grouped.entries()).map(([group, eps]) => {
            const isCollapsed = collapsedGroups.has(group);
            return (
              <Card key={group}>
                <button
                  type="button"
                  className="flex w-full items-center justify-between px-6 py-4 text-left"
                  onClick={() => {
                    toggleGroup(group);
                  }}
                >
                  <div className="flex items-center gap-2">
                    {isCollapsed ? (
                      <ChevronRight className="h-4 w-4 text-muted-foreground" />
                    ) : (
                      <ChevronDown className="h-4 w-4 text-muted-foreground" />
                    )}
                    <span className="font-semibold">{group}</span>
                    <Badge variant="secondary" className="ml-1">
                      {eps.length}
                    </Badge>
                  </div>
                </button>
                {!isCollapsed && (
                  <CardContent className="pt-0">
                    <div className="divide-y">
                      {eps.map((ep) => (
                        <div
                          key={`${ep.method}-${ep.path}`}
                          className="flex flex-wrap items-center gap-2 py-2.5"
                        >
                          <Badge
                            variant="outline"
                            className={`w-16 justify-center font-mono text-xs ${getMethodColor(ep.method)}`}
                          >
                            {ep.method}
                          </Badge>
                          <span className="font-mono text-sm">{ep.path}</span>
                          <span className="text-sm text-muted-foreground">
                            {ep.description}
                          </span>
                          {ep.permission && (
                            <Badge
                              variant="outline"
                              className="ml-auto text-xs text-muted-foreground"
                            >
                              {ep.permission}
                            </Badge>
                          )}
                        </div>
                      ))}
                    </div>
                  </CardContent>
                )}
              </Card>
            );
          })}
        </div>
      )}
    </div>
  );
}
