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
import {
  CephSecretField,
  ISCSITargetField,
  NodeRestrictionField,
} from "./StorageFormFields";
import { PBSEncryptionField } from "./PBSEncryptionField";
import type {
  StorageType,
  StorageContentType,
  StorageConfigResponse,
} from "../types/storage";
import {
  CEPH_SECRET_FIELD,
  STORAGE_TYPE_LABELS,
  STORAGE_TYPE_FIELDS,
  STORAGE_TYPE_CONTENT,
} from "../types/storage";
import {
  initialPBSEncryption,
  pbsEncryptionReady,
  pbsEncryptionRequest,
  pbsKeyStatus,
  type PBSEncryptionChoice,
} from "../lib/pbs-encryption";

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
  const [initialParams, setInitialParams] = useState<Record<string, string>>(
    {},
  );
  const [selectedContent, setSelectedContent] = useState<
    Set<StorageContentType>
  >(new Set());
  const [nodes, setNodes] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [useLuns, setUseLuns] = useState(true);
  const [encryption, setEncryption] = useState<PBSEncryptionChoice>(() =>
    initialPBSEncryption(false),
  );
  const [cephSecret, setCephSecret] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [initialized, setInitialized] = useState(false);

  const configQuery = useStorageConfig(
    open ? clusterId : "",
    open ? storageId : "",
  );
  const updateMutation = useUpdateStorage();

  const sType = storageType as StorageType;
  // See AddStorageDialog: PVE carries "use LUNs directly" in the content field,
  // and offers it for the kernel iSCSI plugin only.
  const isISCSI = sType === "iscsi";
  const typeFields = useMemo(() => STORAGE_TYPE_FIELDS[sType], [sType]);
  const availableContent = STORAGE_TYPE_CONTENT[sType];
  const typeLabel = STORAGE_TYPE_LABELS[sType];
  const isPBS = sType === "pbs";
  // Only an external Ceph cluster takes a credential here; see CEPH_SECRET_FIELD.
  const cephSecretDef = CEPH_SECRET_FIELD[sType];
  const showCephSecret =
    cephSecretDef !== undefined && (params["monhost"] ?? "").trim() !== "";
  // A storage that loaded without monitors is the cluster's own Ceph, whose
  // credential Proxmox derived from the local admin keyring when it was
  // added. Pointing it at another cluster needs that cluster's credential, so
  // an empty field cannot mean "keep the one Proxmox has" here.
  const cephSecretRequired =
    showCephSecret && (initialParams["monhost"] ?? "").trim() === "";

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
      setInitialParams(p);

      // Parse content types
      const content = cfg.content ?? "";
      const contentSet = new Set<StorageContentType>(
        content.split(",").filter(Boolean) as StorageContentType[],
      );
      setSelectedContent(contentSet);

      setNodes(cfg.nodes ?? "");
      // Proxmox omits `disable` entirely when the storage is enabled.
      setEnabled(getConfigValue(cfg, "disable") !== "1");
      // An iSCSI storage with no explicit content is "images" by default.
      setUseLuns(content === "" || contentSet.has("images"));
      // The key itself is never loaded — the read carries only its
      // fingerprint — so the choice starts at "leave it alone".
      setEncryption(
        initialPBSEncryption(pbsKeyStatus(cfg["encryption-key"]) !== null),
      );
      setCephSecret("");
      setInitialized(true);
    }
  }, [configQuery.data, initialized, typeFields]);

  const hasChanges = useMemo(() => {
    if (!configQuery.data) return false;
    return true; // Allow submit anytime form is loaded
  }, [configQuery.data]);

  // The one way the editor closes, whatever closes it: Cancel does not go
  // through the dialog's onOpenChange, and a Cancel that skipped this would
  // bring an abandoned choice — Remove the key, say — back on the next open,
  // ready to be sent with an unrelated change.
  function closeEditor() {
    setOpen(false);
    setInitialized(false);
    setError(null);
    setEncryption(initialPBSEncryption(false));
    setCephSecret("");
    updateMutation.reset();
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
    const cfg = configQuery.data;
    if (!cfg) return;
    setError(null);

    const submitParams: Record<string, string> = {};

    // Only include type-specific fields whose value changed from the loaded
    // config. Proxmox's PUT /storage/{id} schema rejects backend-identifying
    // fields like NFS `export` even when echoed back unchanged — sending them
    // would fail validation, so only forward what the user actually edited.
    for (const field of typeFields) {
      if (field.fixed) continue;
      const v = params[field.key];
      if (
        v !== undefined &&
        v !== "" &&
        v !== (initialParams[field.key] ?? "")
      ) {
        submitParams[field.key] = v;
      }
    }

    const currentContent = cfg.content ?? "";
    if (isISCSI) {
      const newContent = useLuns ? "images" : "none";
      if (newContent !== currentContent) {
        submitParams["content"] = newContent;
      }
    } else {
      const newContent = Array.from(selectedContent).join(",");
      if (newContent !== currentContent && selectedContent.size > 0) {
        submitParams["content"] = newContent;
      }
    }

    // Params Proxmox only clears via its `delete` list — an empty value is
    // dropped by the API layer, so "no nodes" has to be asked for explicitly.
    const deleteKeys: string[] = [];

    const newNodes = nodes.trim();
    const currentNodes = cfg.nodes ?? "";
    if (newNodes !== currentNodes) {
      if (newNodes === "") {
        deleteKeys.push("nodes");
      } else {
        submitParams["nodes"] = newNodes;
      }
    }

    const wasEnabled = getConfigValue(cfg, "disable") !== "1";
    if (enabled !== wasEnabled) {
      submitParams["disable"] = enabled ? "0" : "1";
    }

    // Never pre-filled, so anything here was typed: send it only then, and
    // only while the field is showing.
    if (showCephSecret && cephSecret.trim() !== "") {
      submitParams["keyring"] = cephSecret;
    }

    // Keep sends nothing about the key. A key Proxmox generates for
    // "autogen" is not handled here: useUpdateStorage hands it to the
    // app-wide must-save dialog (usePBSKeyStore), which outlives this dialog
    // and the page it is on.
    if (isPBS) {
      const request = pbsEncryptionRequest(encryption);
      if (request.param !== undefined) {
        submitParams["encryption-key"] = request.param;
      }
      if (request.remove) {
        deleteKeys.push("encryption-key");
      }
    }

    if (Object.keys(submitParams).length === 0 && deleteKeys.length === 0) {
      setError("No changes to save");
      return;
    }

    updateMutation.mutate(
      {
        clusterId,
        storageId,
        data: {
          params: submitParams,
          ...(deleteKeys.length > 0 ? { delete: deleteKeys.join(",") } : {}),
        },
      },
      {
        onSuccess: () => {
          // closeEditor also drops the mutation's copy of the request and
          // response, either of which can hold a key; by now any generated
          // one is in usePBSKeyStore.
          closeEditor();
        },
        onError: (err) => {
          setError(
            err instanceof Error ? err.message : "Failed to update storage",
          );
        },
      },
    );
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (v) setOpen(true);
        else closeEditor();
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
                  {field.required && (
                    <span className="ml-1 text-destructive">*</span>
                  )}
                </Label>
                {field.fixed ? (
                  <>
                    <Input
                      id={`edit-${field.key}`}
                      value={params[field.key] ?? ""}
                      disabled
                    />
                    <p className="text-xs text-muted-foreground">
                      Set when the storage was created — Proxmox does not allow
                      changing it.
                    </p>
                  </>
                ) : field.scan === "iscsi" ? (
                  <ISCSITargetField
                    id={`edit-${field.key}`}
                    clusterId={clusterId}
                    portal={params[field.scanFrom ?? "portal"] ?? ""}
                    value={params[field.key] ?? ""}
                    onChange={(v) => {
                      handleParamChange(field.key, v);
                    }}
                    placeholder={field.placeholder}
                  />
                ) : field.type === "select" && field.options ? (
                  <Select
                    value={params[field.key] ?? ""}
                    onValueChange={(v) => {
                      handleParamChange(field.key, v === "_empty" ? "" : v);
                    }}
                  >
                    <SelectTrigger id={`edit-${field.key}`}>
                      <SelectValue placeholder="Select..." />
                    </SelectTrigger>
                    <SelectContent>
                      {field.options.map((opt) => (
                        <SelectItem
                          key={opt.value}
                          value={opt.value || "_empty"}
                        >
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
                    <Label
                      htmlFor={`edit-${field.key}`}
                      className="text-sm font-normal"
                    >
                      {field.help ?? "Enable"}
                    </Label>
                  </div>
                ) : (
                  <Input
                    id={`edit-${field.key}`}
                    type={
                      field.type === "password"
                        ? "password"
                        : field.type === "number"
                          ? "number"
                          : "text"
                    }
                    value={params[field.key] ?? ""}
                    onChange={(e) => {
                      handleParamChange(field.key, e.target.value);
                    }}
                    // A storage credential, never the Nexara login a browser
                    // would otherwise offer to fill in.
                    autoComplete={
                      field.type === "password" ? "new-password" : undefined
                    }
                    placeholder={field.placeholder}
                  />
                )}
              </div>
            ))}

            {cephSecretDef && showCephSecret && (
              <CephSecretField
                id="edit-ceph-secret"
                def={cephSecretDef}
                value={cephSecret}
                onChange={setCephSecret}
                editing={!cephSecretRequired}
                required={cephSecretRequired}
              />
            )}

            {isPBS && (
              <PBSEncryptionField
                idPrefix="edit"
                storage={storageName}
                current={configQuery.data["encryption-key"]}
                value={encryption}
                onChange={setEncryption}
              />
            )}

            {/* Content Types (iSCSI expresses its single choice as the LUNs toggle) */}
            {isISCSI ? (
              <div className="space-y-1.5">
                <div className="flex items-center gap-2">
                  <Checkbox
                    id="edit-storage-luns"
                    checked={useLuns}
                    onCheckedChange={(checked) => {
                      setUseLuns(checked === true);
                    }}
                  />
                  <Label
                    htmlFor="edit-storage-luns"
                    className="text-sm font-normal"
                  >
                    Use LUNs directly
                  </Label>
                </div>
                <p className="text-xs text-muted-foreground">
                  Attach the target&apos;s LUNs to guests as disks. Turn off to
                  use the target only as a base for LVM on top of it.
                </p>
              </div>
            ) : (
              <div className="space-y-1.5">
                <Label>Content Types</Label>
                <div className="flex flex-wrap gap-2">
                  {ALL_CONTENT_TYPES.filter((ct) =>
                    availableContent.includes(ct.value),
                  ).map((ct) => (
                    <Badge
                      key={ct.value}
                      variant={
                        selectedContent.has(ct.value) ? "default" : "outline"
                      }
                      className="cursor-pointer select-none"
                      onClick={() => {
                        toggleContent(ct.value);
                      }}
                    >
                      {ct.label}
                    </Badge>
                  ))}
                </div>
              </div>
            )}

            {/* Nodes */}
            <div className="space-y-1.5">
              <Label htmlFor="edit-nodes">Nodes (optional)</Label>
              <NodeRestrictionField
                id="edit-nodes"
                clusterId={clusterId}
                value={nodes}
                onChange={setNodes}
              />
              <p className="text-xs text-muted-foreground">
                Restrict storage to specific cluster nodes.
              </p>
            </div>

            {/* Enabled */}
            <div className="space-y-1.5">
              <div className="flex items-center gap-2">
                <Checkbox
                  id="edit-storage-enabled"
                  checked={enabled}
                  onCheckedChange={(checked) => {
                    setEnabled(checked === true);
                  }}
                />
                <Label
                  htmlFor="edit-storage-enabled"
                  className="text-sm font-normal"
                >
                  Enable
                </Label>
              </div>
              <p className="text-xs text-muted-foreground">
                Disabled storage stays configured but is not mounted or used by
                any node.
              </p>
            </div>

            {error && <p className="text-sm text-destructive">{error}</p>}

            <div className="flex justify-end gap-2 pt-2">
              {/* Not disabled while the save runs: closing then is safe, as
                  it is by Escape or the close button — the key Proxmox
                  generates still reaches the must-save dialog. */}
              <Button variant="outline" onClick={closeEditor}>
                Cancel
              </Button>
              <Button
                onClick={handleSubmit}
                disabled={
                  !hasChanges ||
                  updateMutation.isPending ||
                  (isPBS && !pbsEncryptionReady(encryption)) ||
                  (cephSecretRequired && !cephSecret.trim())
                }
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
