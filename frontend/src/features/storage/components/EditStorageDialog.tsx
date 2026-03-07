import { useState, useEffect, useMemo } from "react";
import { Pencil } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Checkbox } from "@/components/ui/checkbox";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { useStorageConfig, useUpdateStorage } from "../api/storage-queries";
import type {
  StorageType,
  StorageContentType,
  StorageConfigResponse,
} from "../types/storage";
import {
  STORAGE_TYPE_LABELS,
  STORAGE_TYPE_FIELDS,
  STORAGE_TYPE_CONTENT,
} from "../types/storage";

interface EditStorageDialogProps {
  clusterId: string;
  storageId: string;
  storageName: string;
  storageType: string;
}

const ALL_CONTENT_TYPES: { value: StorageContentType; label: string }[] = [
  { value: "images", label: "Disk Images" },
  { value: "rootdir", label: "Container" },
  { value: "iso", label: "ISO Image" },
  { value: "vztmpl", label: "CT Template" },
  { value: "backup", label: "Backup" },
  { value: "snippets", label: "Snippets" },
];

function getConfigValue(cfg: StorageConfigResponse, key: string): string {
  const record = cfg as unknown as Record<string, unknown>;
  const val = record[key];
  if (val === undefined || val === null) return "";
  if (typeof val === "string") return val;
  if (typeof val === "number") return String(val);
  if (typeof val === "boolean") return val ? "1" : "0";
  return "";
}

export function EditStorageDialog({
  clusterId,
  storageId,
  storageName,
  storageType,
}: EditStorageDialogProps) {
  const [open, setOpen] = useState(false);
  const [params, setParams] = useState<Record<string, string>>({});
  const [selectedContent, setSelectedContent] = useState<Set<StorageContentType>>(new Set());
  const [nodes, setNodes] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [initialized, setInitialized] = useState(false);

  const configQuery = useStorageConfig(
    open ? clusterId : "",
    open ? storageId : "",
  );
  const updateMutation = useUpdateStorage();

  const sType = storageType as StorageType;
  const typeFields = useMemo(() => STORAGE_TYPE_FIELDS[sType], [sType]);
  const availableContent = STORAGE_TYPE_CONTENT[sType];
  const typeLabel = STORAGE_TYPE_LABELS[sType];

  // Initialize form from config when loaded
  useEffect(() => {
    if (configQuery.data && !initialized) {
      const cfg = configQuery.data;
      const p: Record<string, string> = {};
      for (const field of typeFields) {
        const v = getConfigValue(cfg, field.key);
        if (v) p[field.key] = v;
      }
      setParams(p);

      // Parse content types
      const content = cfg.content ?? "";
      const contentSet = new Set<StorageContentType>(
        content.split(",").filter(Boolean) as StorageContentType[],
      );
      setSelectedContent(contentSet);

      setNodes(cfg.nodes ?? "");
      setInitialized(true);
    }
  }, [configQuery.data, initialized, typeFields]);

  const hasChanges = useMemo(() => {
    if (!configQuery.data) return false;
    return true; // Allow submit anytime form is loaded
  }, [configQuery.data]);

  function handleParamChange(key: string, value: string) {
    setParams((prev) => ({ ...prev, [key]: value }));
  }

  function toggleContent(ct: StorageContentType) {
    setSelectedContent((prev) => {
      const next = new Set(prev);
      if (next.has(ct)) {
        next.delete(ct);
      } else {
        next.add(ct);
      }
      return next;
    });
  }

  function handleSubmit() {
    setError(null);

    const submitParams: Record<string, string> = {};

    // Only include type-specific fields that have values
    for (const field of typeFields) {
      const v = params[field.key];
      if (v !== undefined && v !== "") {
        submitParams[field.key] = v;
      }
    }

    if (selectedContent.size > 0) {
      submitParams["content"] = Array.from(selectedContent).join(",");
    }

    if (nodes.trim()) {
      submitParams["nodes"] = nodes.trim();
    }

    updateMutation.mutate(
      {
        clusterId,
        storageId,
        data: { params: submitParams },
      },
      {
        onSuccess: () => {
          setOpen(false);
          setInitialized(false);
        },
        onError: (err) => {
          setError(err instanceof Error ? err.message : "Failed to update storage");
        },
      },
    );
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        setOpen(v);
        if (!v) {
          setInitialized(false);
          setError(null);
          updateMutation.reset();
        }
      }}
    >
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          <Pencil className="mr-1 h-3.5 w-3.5" />
          Edit
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            Edit Storage: {storageName}
            <Badge variant="outline">{typeLabel}</Badge>
          </DialogTitle>
        </DialogHeader>

        {configQuery.isLoading && (
          <div className="space-y-3 pt-2">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        )}

        {configQuery.isError && (
          <p className="py-4 text-sm text-destructive">
            Failed to load storage configuration.
          </p>
        )}

        {configQuery.data && (
          <div className="space-y-4 pt-2">
            {/* Storage name (read-only) */}
            <div className="space-y-1.5">
              <Label>ID</Label>
              <Input value={storageName} disabled />
            </div>

            {/* Type (read-only) */}
            <div className="space-y-1.5">
              <Label>Type</Label>
              <Input value={typeLabel} disabled />
            </div>

            {/* Type-specific fields */}
            {typeFields.map((field) => (
              <div key={field.key} className="space-y-1.5">
                <Label htmlFor={`edit-${field.key}`}>
                  {field.label}
                  {field.required && <span className="ml-1 text-destructive">*</span>}
                </Label>
                {field.type === "select" && field.options ? (
                  <Select
                    value={params[field.key] ?? ""}
                    onValueChange={(v) => { handleParamChange(field.key, v === "_empty" ? "" : v); }}
                  >
                    <SelectTrigger id={`edit-${field.key}`}>
                      <SelectValue placeholder="Select..." />
                    </SelectTrigger>
                    <SelectContent>
                      {field.options.map((opt) => (
                        <SelectItem key={opt.value} value={opt.value || "_empty"}>
                          {opt.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                ) : field.type === "checkbox" ? (
                  <div className="flex items-center gap-2">
                    <Checkbox
                      id={`edit-${field.key}`}
                      checked={params[field.key] === "1"}
                      onCheckedChange={(checked) => {
                        handleParamChange(field.key, checked ? "1" : "0");
                      }}
                    />
                    <Label htmlFor={`edit-${field.key}`} className="text-sm font-normal">
                      {field.help ?? "Enable"}
                    </Label>
                  </div>
                ) : (
                  <Input
                    id={`edit-${field.key}`}
                    type={field.type === "password" ? "password" : field.type === "number" ? "number" : "text"}
                    value={params[field.key] ?? ""}
                    onChange={(e) => { handleParamChange(field.key, e.target.value); }}
                    placeholder={field.placeholder}
                  />
                )}
              </div>
            ))}

            {/* Content Types */}
            <div className="space-y-1.5">
              <Label>Content Types</Label>
              <div className="flex flex-wrap gap-2">
                {ALL_CONTENT_TYPES.filter((ct) =>
                  availableContent.includes(ct.value),
                ).map((ct) => (
                  <Badge
                    key={ct.value}
                    variant={selectedContent.has(ct.value) ? "default" : "outline"}
                    className="cursor-pointer select-none"
                    onClick={() => { toggleContent(ct.value); }}
                  >
                    {ct.label}
                  </Badge>
                ))}
              </div>
            </div>

            {/* Nodes */}
            <div className="space-y-1.5">
              <Label htmlFor="edit-nodes">Nodes (optional)</Label>
              <Input
                id="edit-nodes"
                value={nodes}
                onChange={(e) => { setNodes(e.target.value); }}
                placeholder="node1,node2 (leave empty for all)"
              />
            </div>

            {error && (
              <p className="text-sm text-destructive">{error}</p>
            )}

            <div className="flex justify-end gap-2 pt-2">
              <Button
                variant="outline"
                onClick={() => { setOpen(false); }}
                disabled={updateMutation.isPending}
              >
                Cancel
              </Button>
              <Button
                onClick={handleSubmit}
                disabled={!hasChanges || updateMutation.isPending}
              >
                {updateMutation.isPending ? "Saving..." : "Save Changes"}
              </Button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
