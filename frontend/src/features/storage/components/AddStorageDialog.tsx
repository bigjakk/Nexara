import { useState, useMemo } from "react";
import { Plus, HardDrive } from "lucide-react";
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
import { useCreateStorage } from "../api/storage-queries";
import type {
  StorageType,
  StorageContentType,
} from "../types/storage";
import {
  STORAGE_TYPE_LABELS,
  STORAGE_TYPE_FIELDS,
  STORAGE_TYPE_CONTENT,
} from "../types/storage";

interface AddStorageDialogProps {
  clusterId: string;
}

const ALL_STORAGE_TYPES = Object.keys(STORAGE_TYPE_LABELS) as StorageType[];

const ALL_CONTENT_TYPES: { value: StorageContentType; label: string }[] = [
  { value: "images", label: "Disk Images" },
  { value: "rootdir", label: "Container" },
  { value: "iso", label: "ISO Image" },
  { value: "vztmpl", label: "CT Template" },
  { value: "backup", label: "Backup" },
  { value: "snippets", label: "Snippets" },
];

export function AddStorageDialog({ clusterId }: AddStorageDialogProps) {
  const [open, setOpen] = useState(false);
  const [storageType, setStorageType] = useState<StorageType>("dir");
  const [storageName, setStorageName] = useState("");
  const [params, setParams] = useState<Record<string, string>>({});
  const [selectedContent, setSelectedContent] = useState<Set<StorageContentType>>(new Set());
  const [nodes, setNodes] = useState("");
  const [error, setError] = useState<string | null>(null);

  const createMutation = useCreateStorage();

  const typeFields = STORAGE_TYPE_FIELDS[storageType];
  const availableContent = STORAGE_TYPE_CONTENT[storageType];

  const hasAllRequired = useMemo(() => {
    if (!storageName.trim()) return false;
    for (const field of typeFields) {
      if (field.required && !params[field.key]?.trim()) return false;
    }
    return true;
  }, [storageName, params, typeFields]);

  function resetForm() {
    setStorageName("");
    setParams({});
    setSelectedContent(new Set());
    setNodes("");
    setError(null);
    createMutation.reset();
  }

  function handleTypeChange(newType: StorageType) {
    setStorageType(newType);
    setParams({});
    // Pre-select all available content types for new storage
    setSelectedContent(new Set(STORAGE_TYPE_CONTENT[newType]));
    setError(null);
  }

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
    if (!hasAllRequired) return;
    setError(null);

    const submitParams: Record<string, string> = { ...params };

    // Add content types
    if (selectedContent.size > 0) {
      submitParams["content"] = Array.from(selectedContent).join(",");
    }

    // Add nodes restriction
    if (nodes.trim()) {
      submitParams["nodes"] = nodes.trim();
    }

    createMutation.mutate(
      {
        clusterId,
        data: {
          storage: storageName.trim(),
          type: storageType,
          params: submitParams,
        },
      },
      {
        onSuccess: () => {
          resetForm();
          setOpen(false);
        },
        onError: (err) => {
          setError(err instanceof Error ? err.message : "Failed to create storage");
        },
      },
    );
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        setOpen(v);
        if (v) {
          resetForm();
          setStorageType("dir");
          setSelectedContent(new Set(STORAGE_TYPE_CONTENT["dir"]));
        }
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus className="mr-1 h-4 w-4" />
          Add Storage
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <HardDrive className="h-5 w-5" />
            Add Storage
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4 pt-2">
          {/* Storage Name */}
          <div className="space-y-1.5">
            <Label htmlFor="storage-name">ID</Label>
            <Input
              id="storage-name"
              value={storageName}
              onChange={(e) => { setStorageName(e.target.value.replace(/[^a-zA-Z0-9_-]/g, "")); }}
              placeholder="my-storage"
              autoFocus
            />
            <p className="text-xs text-muted-foreground">
              Alphanumeric, dashes, and underscores only.
            </p>
          </div>

          {/* Storage Type */}
          <div className="space-y-1.5">
            <Label>Type</Label>
            <Select
              value={storageType}
              onValueChange={(v) => { handleTypeChange(v as StorageType); }}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ALL_STORAGE_TYPES.map((t) => (
                  <SelectItem key={t} value={t}>
                    {STORAGE_TYPE_LABELS[t]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {/* Type-specific fields */}
          {typeFields.map((field) => (
            <div key={field.key} className="space-y-1.5">
              <Label htmlFor={`field-${field.key}`}>
                {field.label}
                {field.required && <span className="ml-1 text-destructive">*</span>}
              </Label>
              {field.type === "select" && field.options ? (
                <Select
                  value={params[field.key] ?? ""}
                  onValueChange={(v) => { handleParamChange(field.key, v); }}
                >
                  <SelectTrigger id={`field-${field.key}`}>
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
                    id={`field-${field.key}`}
                    checked={params[field.key] === "1"}
                    onCheckedChange={(checked) => {
                      handleParamChange(field.key, checked ? "1" : "0");
                    }}
                  />
                  <Label htmlFor={`field-${field.key}`} className="text-sm font-normal">
                    {field.help ?? "Enable"}
                  </Label>
                </div>
              ) : (
                <Input
                  id={`field-${field.key}`}
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

          {/* Nodes restriction */}
          <div className="space-y-1.5">
            <Label htmlFor="storage-nodes">Nodes (optional)</Label>
            <Input
              id="storage-nodes"
              value={nodes}
              onChange={(e) => { setNodes(e.target.value); }}
              placeholder="node1,node2 (leave empty for all)"
            />
            <p className="text-xs text-muted-foreground">
              Restrict storage to specific cluster nodes.
            </p>
          </div>

          {/* Error */}
          {error && (
            <p className="text-sm text-destructive">{error}</p>
          )}

          {/* Submit */}
          <div className="flex justify-end gap-2 pt-2">
            <Button
              variant="outline"
              onClick={() => { setOpen(false); }}
              disabled={createMutation.isPending}
            >
              Cancel
            </Button>
            <Button
              onClick={handleSubmit}
              disabled={!hasAllRequired || createMutation.isPending}
            >
              {createMutation.isPending ? "Creating..." : "Create Storage"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
