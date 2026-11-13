import { useState, useCallback } from "react";
import { Search, X, HelpCircle } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { parseQuery } from "../lib/search-parser";
import type { ParsedQuery } from "../types/inventory";

interface SearchBarProps {
  onQueryChange: (parsed: ParsedQuery) => void;
}

function SearchHelp() {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          aria-label="Search syntax help"
          type="button"
          className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
        >
          <HelpCircle className="h-4 w-4" />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-80 text-sm" align="end">
        <h4 className="mb-2 font-medium">Search syntax</h4>
        <div className="space-y-2.5 text-xs text-muted-foreground">
          <div>
            <span className="font-medium text-foreground">Filters</span>
            <div className="mt-1 space-y-0.5">
              <div>
                <code>type:vm</code> — vm, ct, lxc, qemu, node
              </div>
              <div>
                <code>status:running</code> — running, stopped, paused, online…
              </div>
              <div>
                <code>cluster:prod</code> — cluster name contains "prod"
              </div>
              <div>
                <code>node:pve1</code> — node name contains "pve1"
              </div>
              <div>
                <code>name:web</code> — resource name contains "web"
              </div>
              <div>
                <code>tags:prod</code> — tag contains "prod"
              </div>
              <div>
                <code>pool:dev</code> — pool name contains "dev"
              </div>
              <div>
                <code>ha:started</code> — HA state
              </div>
              <div>
                <code>vmid:100</code> — exact VMID
              </div>
              <div>
                <code>template:true</code> — templates only
              </div>
            </div>
          </div>
          <div>
            <span className="font-medium text-foreground">Comparisons</span>
            <div className="mt-1 space-y-0.5">
              <div>
                <code>cpu&gt;80%</code> <code>mem&lt;50%</code> — usage
                thresholds
              </div>
              <div>
                <code>cpus&gt;4</code> — CPU core count
              </div>
              <div>
                <code>uptime&gt;1d</code> — duration (s/m/h/d/w)
              </div>
            </div>
          </div>
          <div>
            <span className="font-medium text-foreground">Advanced</span>
            <div className="mt-1 space-y-0.5">
              <div>
                <code>status:running,paused</code> — match any (OR)
              </div>
              <div>
                <code>!status:stopped</code> — negation
              </div>
              <div>
                <code>name:&quot;my server&quot;</code> — quoted values
              </div>
              <div>
                <code>web-01</code> — free text (name, cluster, node, tags)
              </div>
            </div>
          </div>
          <p className="pt-1 text-[11px] text-muted-foreground/70">
            Multiple filters use AND logic
          </p>
        </div>
      </PopoverContent>
    </Popover>
  );
}

/** How the search box read the query: one chip per filter it recognised. */
export function SearchFilterChips({ query }: { query: ParsedQuery }) {
  if (query.filters.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-1">
      {query.filters.map((filter, i) => {
        let label: string;
        if (filter.operator === "eq") {
          label = `${filter.field}:${filter.value}`;
        } else if (filter.operator === "neq") {
          label = `!${filter.field}:${filter.value}`;
        } else {
          label = `${filter.field}${filter.operator === "gt" ? ">" : "<"}${filter.value}`;
        }
        return (
          <Badge key={i} variant="secondary" className="text-xs">
            {label}
          </Badge>
        );
      })}
      {query.freeText && (
        <Badge variant="secondary" className="text-xs">
          &quot;{query.freeText}&quot;
        </Badge>
      )}
    </div>
  );
}

export function SearchBar({ onQueryChange }: SearchBarProps) {
  const [value, setValue] = useState("");

  const handleChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const newValue = e.target.value;
      setValue(newValue);
      onQueryChange(parseQuery(newValue));
    },
    [onQueryChange],
  );

  const handleClear = useCallback(() => {
    setValue("");
    onQueryChange(parseQuery(""));
  }, [onQueryChange]);

  return (
    <div className="relative">
      <Search className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        aria-label="Search resources"
        placeholder="Search, or filter with type:vm cpu>80%"
        value={value}
        onChange={handleChange}
        className="h-8 pl-8 pr-14"
      />
      {value ? (
        <button
          aria-label="Clear search"
          type="button"
          onClick={handleClear}
          className="absolute right-8 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
        >
          <X className="h-4 w-4" />
        </button>
      ) : null}
      <SearchHelp />
    </div>
  );
}
