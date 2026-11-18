import { useState, useMemo } from "react";
import { Plus } from "lucide-react";
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
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import {
  CephSecretField,
  ISCSITargetField,
  NodeRestrictionField,
} from "./StorageFormFields";
import { PBSEncryptionField } from "./PBSEncryptionField";
import { PBSDatastoreField, PBSServerSource } from "./PBSServerSource";
import { usePermissions } from "@/hooks/usePermissions";
import type { StorageType, StorageContentType } from "../types/storage";
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
  type PBSEncryptionChoice,
} from "../lib/pbs-encryption";
import {
  pbsSourceRequest,
  pbsSuppliedFields,
  type PBSSource,
} from "../lib/pbs-source";
import { useTranslation } from "react-i18next";

interface AddStorageDialogProps {
  clusterId: string;
  /** Named in the title, for a page that shows more than one cluster. */
  clusterName?: string | undefined;
  /**
   * How the built-in trigger is drawn: a page's one primary button, or an
   * outline button where several triggers share a screen.
   */
  triggerVariant?: "default" | "outline";
  /**
   * When provided, the dialog runs in controlled mode and the built-in
   * trigger button is hidden. The caller is responsible for opening it
   * (e.g. from a context menu item).
   */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}

const ALL_STORAGE_TYPES = Object.keys(STORAGE_TYPE_LABELS) as StorageType[];

/**
 * What the ID field keeps of what is typed, or of a datastore's name: the
 * characters a Proxmox storage id may hold (the storage-id format).
 */
function storageID(value: string): string {
  return value.replace(/[^a-zA-Z0-9._-]/g, "");
}

const ALL_CONTENT_TYPES: { value: StorageContentType; label: string }[] = [
  { value: "images", label: "Disk images" },
  { value: "rootdir", label: "Container volumes" },
  { value: "iso", label: "ISO images" },
  { value: "vztmpl", label: "CT templates" },
  { value: "backup", label: "Backups" },
  { value: "snippets", label: "Snippets" },
];

export function AddStorageDialog({
  clusterId,
  clusterName,
  triggerVariant = "default",
  open: openProp,
  onOpenChange,
}: AddStorageDialogProps) {
  const { t } = useTranslation("storage");
  const [internalOpen, setInternalOpen] = useState(false);
  const controlled = openProp !== undefined;
  const open = controlled ? openProp : internalOpen;
  const setOpen = (v: boolean) => {
    if (!controlled) setInternalOpen(v);
    onOpenChange?.(v);
  };
  const [storageType, setStorageType] = useState<StorageType>("dir");
  const [storageName, setStorageName] = useState("");
  const [params, setParams] = useState<Record<string, string>>({});
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
  // A PBS server registered under Backup, picked to fill the PBS fields in.
  const [pbsSource, setPbsSource] = useState<PBSSource | null>(null);
  // Until the ID is typed, a PBS storage's follows its datastore: backup
  // coverage ties the two by name (internal/reports/gen_backup.go).
  const [idTyped, setIdTyped] = useState(false);

  const createMutation = useCreateStorage();
  // The dialog shows a failure itself (the hook opts out of the app's toast),
  // and one that lands after it was closed is toasted instead.
  const settle = useSaveOutcome(open);
  const { hasPermission } = usePermissions();

  // PVE models "use LUNs directly" as the content type rather than a flag of its
  // own, and offers it for the kernel iSCSI plugin only — iSCSI Direct always
  // exposes its LUNs as disk images.
  const isISCSI = storageType === "iscsi";

  const typeFields = STORAGE_TYPE_FIELDS[storageType];
  const availableContent = STORAGE_TYPE_CONTENT[storageType];
  const isPBS = storageType === "pbs";
  const source = isPBS ? pbsSource : null;
  const supplied = useMemo(() => pbsSuppliedFields(source), [source]);
  const datastore = params["datastore"] ?? "";
  // Only an external Ceph cluster takes a credential here; see CEPH_SECRET_FIELD.
  const cephSecretDef = CEPH_SECRET_FIELD[storageType];
  const showCephSecret =
    cephSecretDef !== undefined && (params["monhost"] ?? "").trim() !== "";

  const hasAllRequired = useMemo(() => {
    if (!storageName.trim()) return false;
    for (const field of typeFields) {
      if (supplied.has(field.key)) continue;
      if (field.required && !params[field.key]?.trim()) return false;
    }
    // Required once showing, as the Proxmox GUI has it (allowBlank: false in
    // RBDEdit.js and CephFSEdit.js): left out, Proxmox would copy this
    // cluster's own admin keyring for a storage on another cluster.
    if (showCephSecret && !cephSecret.trim()) return false;
    if (isPBS && !pbsEncryptionReady(encryption)) return false;
    return true;
  }, [
    storageName,
    params,
    typeFields,
    supplied,
    showCephSecret,
    cephSecret,
    isPBS,
    encryption,
  ]);

  function resetForm() {
    setStorageName("");
    setParams({});
    setSelectedContent(new Set());
    setNodes("");
    setEnabled(true);
    setUseLuns(true);
    setEncryption(initialPBSEncryption(false));
    setCephSecret("");
    setError(null);
    setPbsSource(null);
    setIdTyped(false);
    createMutation.reset();
  }

  // A pasted key, a Ceph credential, a typed password and a failed request's
  // variables are not kept once the dialog closes. Opened from the storage
  // tree's context menu the dialog is controlled, and reopening it that way
  // does not reset the form, so they would otherwise come straight back. The
  // rest of the form is kept, as it always was.
  function forgetSecrets() {
    setEncryption(initialPBSEncryption(false));
    setCephSecret("");
    const passwords = new Set(
      typeFields.filter((f) => f.type === "password").map((f) => f.key),
    );
    setParams((prev) =>
      Object.fromEntries(
        Object.entries(prev).filter(([key]) => !passwords.has(key)),
      ),
    );
    createMutation.reset();
  }

  function handleTypeChange(newType: StorageType) {
    setStorageType(newType);
    setParams({});
    // Pre-select all available content types for new storage
    setSelectedContent(new Set(STORAGE_TYPE_CONTENT[newType]));
    setUseLuns(true);
    setEncryption(initialPBSEncryption(false));
    setCephSecret("");
    setError(null);
    setPbsSource(null);
  }

  function handleParamChange(key: string, value: string) {
    setParams((prev) => ({ ...prev, [key]: value }));
    if (isPBS && key === "datastore" && !idTyped) {
      setStorageName(storageID(value));
    }
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

    // Add content types. For iSCSI the choice is binary and carried by the LUNs
    // toggle: "images" hands the LUNs to guests as disks, "none" leaves the
    // target as a raw base for an LVM group stacked on top of it.
    if (isISCSI) {
      submitParams["content"] = useLuns ? "images" : "none";
    } else if (selectedContent.size > 0) {
      submitParams["content"] = Array.from(selectedContent).join(",");
    }

    // Add nodes restriction
    if (nodes.trim()) {
      submitParams["nodes"] = nodes.trim();
    }

    // Only sent when off: Proxmox defaults new storage to enabled.
    if (!enabled) {
      submitParams["disable"] = "1";
    }

    // Sent only when typed, and only while the field is showing: a keyring
    // typed before Monitor hosts was cleared again must not go along.
    if (showCephSecret && cephSecret.trim() !== "") {
      submitParams["keyring"] = cephSecret;
    }

    // A key Proxmox generates for "autogen" is not handled here: useCreateStorage
    // hands it to the app-wide must-save dialog (usePBSKeyStore), which
    // outlives this dialog and the page it is on.
    if (isPBS) {
      const request = pbsEncryptionRequest(encryption);
      if (request.param !== undefined) {
        submitParams["encryption-key"] = request.param;
      }
    }

    // From a picked PBS server, its own fields go as it says, whatever the
    // form held for them before it was picked.
    const fromSource =
      source === null
        ? { params: submitParams }
        : pbsSourceRequest(source, submitParams);

    settle(
      createMutation.mutateAsync({
        clusterId,
        data: {
          storage: storageName.trim(),
          type: storageType,
          ...fromSource,
        },
      }),
      {
        action: t("addStorageDialogActions.addingStorageStorageName", {
          storageName: storageName.trim(),
        }),
        onSuccess: () => {
          resetForm();
          setOpen(false);
        },
        onError: (err) => {
          setError(
            err instanceof Error ? err.message : "Failed to create storage",
          );
        },
      },
    );
  }

  // Adding a store takes manage:storage on the cluster, which the create
  // handler checks; without it there is no Add storage at all.
  if (!hasPermission("manage", "storage")) return null;

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        setOpen(v);
        if (v) {
          resetForm();
          setStorageType("dir");
          setSelectedContent(new Set(STORAGE_TYPE_CONTENT["dir"]));
        } else {
          forgetSecrets();
        }
      }}
    >
      {!controlled && (
        <DialogTrigger asChild>
          <Button size="sm" variant={triggerVariant} className="gap-1.5">
            <Plus className="h-4 w-4" />
            Add storage
          </Button>
        </DialogTrigger>
      )}
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex flex-wrap items-baseline gap-x-2.5">
            Add storage
            {clusterName !== undefined && (
              <span className="text-sm font-normal text-muted-foreground">
                to {clusterName}
              </span>
            )}
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4 pt-2">
          {/* Storage Name */}
          <div className="space-y-1.5">
            <Label htmlFor="storage-name">ID</Label>
            <Input
              id="storage-name"
              value={storageName}
              onChange={(e) => {
                const id = storageID(e.target.value);
                setStorageName(id);
                setIdTyped(id !== "");
              }}
              placeholder="my-storage"
              autoFocus
            />
            <p className="text-xs text-muted-foreground">
              Letters, digits, dots, dashes and underscores.
            </p>
            {isPBS &&
              datastore !== "" &&
              storageName !== "" &&
              storageName !== datastore && (
                <p className="text-xs text-muted-foreground">
                  Backup coverage ties a PBS storage to its datastore by ID:
                  with an ID other than &ldquo;{datastore}&rdquo;, backups made
                  to it won&apos;t count.
                </p>
              )}
          </div>

          {/* Storage Type */}
          <div className="space-y-1.5">
            <Label>Type</Label>
            <Select
              value={storageType}
              onValueChange={(v) => {
                handleTypeChange(v as StorageType);
              }}
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

          {isPBS && (
            <PBSServerSource
              value={pbsSource}
              onChange={(next) => {
                // A datastore picked for one server is not another's.
                if (next?.server.id !== pbsSource?.server.id) {
                  handleParamChange("datastore", "");
                }
                setPbsSource(next);
              }}
              clusterId={clusterId}
              canManagePBS={hasPermission("manage", "pbs")}
            />
          )}

          {/* Type-specific fields */}
          {typeFields
            .filter((field) => !supplied.has(field.key))
            .map((field) => (
              <div key={field.key} className="space-y-1.5">
                <Label htmlFor={`field-${field.key}`}>
                  {field.label}
                  {field.required && (
                    <span className="ml-1 text-destructive">*</span>
                  )}
                </Label>
                {source !== null && field.key === "datastore" ? (
                  <PBSDatastoreField
                    id={`field-${field.key}`}
                    serverId={source.server.id}
                    value={datastore}
                    onChange={(v) => {
                      handleParamChange(field.key, v);
                    }}
                  />
                ) : field.scan === "iscsi" ? (
                  <ISCSITargetField
                    id={`field-${field.key}`}
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
                      handleParamChange(field.key, v);
                    }}
                  >
                    <SelectTrigger id={`field-${field.key}`}>
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
                      id={`field-${field.key}`}
                      checked={params[field.key] === "1"}
                      onCheckedChange={(checked) => {
                        handleParamChange(field.key, checked ? "1" : "0");
                      }}
                    />
                    <Label
                      htmlFor={`field-${field.key}`}
                      className="text-sm font-normal"
                    >
                      {field.help ?? "Enable"}
                    </Label>
                  </div>
                ) : (
                  <Input
                    id={`field-${field.key}`}
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
                {field.help !== undefined && field.type !== "checkbox" && (
                  <p className="text-xs text-muted-foreground">{field.help}</p>
                )}
              </div>
            ))}

          {cephSecretDef && showCephSecret && (
            <CephSecretField
              id="field-ceph-secret"
              def={cephSecretDef}
              value={cephSecret}
              onChange={setCephSecret}
              editing={false}
              required
            />
          )}

          {isPBS && (
            <PBSEncryptionField
              idPrefix="add"
              storage={storageName}
              value={encryption}
              onChange={setEncryption}
            />
          )}

          {/* Content Types (iSCSI expresses its single choice as the LUNs toggle) */}
          {isISCSI ? (
            <div className="space-y-1.5">
              <div className="flex items-center gap-2">
                <Checkbox
                  id="storage-luns"
                  checked={useLuns}
                  onCheckedChange={(checked) => {
                    setUseLuns(checked === true);
                  }}
                />
                <Label htmlFor="storage-luns" className="text-sm font-normal">
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
              <Label>Content types</Label>
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

          {/* Nodes restriction */}
          <div className="space-y-1.5">
            <Label htmlFor="storage-nodes">Nodes (optional)</Label>
            <NodeRestrictionField
              id="storage-nodes"
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
                id="storage-enabled"
                checked={enabled}
                onCheckedChange={(checked) => {
                  setEnabled(checked === true);
                }}
              />
              <Label htmlFor="storage-enabled" className="text-sm font-normal">
                Enable
              </Label>
            </div>
            <p className="text-xs text-muted-foreground">
              Disabled storage stays configured but is not mounted or used by
              any node.
            </p>
          </div>

          {/* Error */}
          {error && <p className="text-sm text-destructive">{error}</p>}

          {/* Submit */}
          <div className="flex justify-end gap-2 pt-2">
            {/* Not disabled while the create runs: closing then is safe,
                as it is by Escape or the close button — the key Proxmox
                generates still reaches the must-save dialog. */}
            <Button
              variant="outline"
              onClick={() => {
                setOpen(false);
                forgetSecrets();
              }}
            >
              Cancel
            </Button>
            <Button
              onClick={handleSubmit}
              disabled={!hasAllRequired || createMutation.isPending}
            >
              {createMutation.isPending ? "Creating..." : "Create storage"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
