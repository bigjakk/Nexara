import { useState, useCallback } from "react";
import { Search, X } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { parseQuery } from "../lib/search-parser";
import type { ParsedQuery } from "../types/inventory";

interface SearchBarProps {
  onQueryChange: (parsed: ParsedQuery) => void;
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

  const parsed = parseQuery(value);

  return (
    <div className="space-y-2">
      <div className="relative">
        <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          placeholder="Search... (e.g. type:vm status:running cpu>80%)"
          value={value}
          onChange={handleChange}
          className="pl-9 pr-9"
        />
        {value && (
          <button
            type="button"
            onClick={handleClear}
            className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
          >
            <X className="h-4 w-4" />
          </button>
        )}
      </div>
      {parsed.filters.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {parsed.filters.map((filter, i) => {
            const label =
              filter.operator === "eq"
                ? `${filter.field}:${filter.value}`
                : `${filter.field}${filter.operator === "gt" ? ">" : "<"}${filter.value}%`;
            return (
              <Badge key={i} variant="secondary" className="text-xs">
                {label}
              </Badge>
            );
          })}
          {parsed.freeText && (
            <Badge variant="secondary" className="text-xs">
              &quot;{parsed.freeText}&quot;
            </Badge>
          )}
        </div>
      )}
    </div>
  );
}
