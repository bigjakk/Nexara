import { useEffect, useMemo, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { QueryFailureNote } from "@/components/QueryStateNotice";
import {
  useCreateNetworkInterface,
  useUpdateNetworkInterface,
  useNodeNetworkInterfaces,
} from "../api/network-queries";
import type {
  CreateNetworkInterfaceRequest,
  NetworkInterface,
  NetworkInterfaceOptions,
  UpdateNetworkInterfaceRequest,
} from "../types/network";
import {
  BOND_HASH_POLICIES,
  bondModesFor,
  CREATABLE_INTERFACE_TYPES,
  clearedSettings,
  defaultBondMode,
  fieldsForType,
  interfaceTypeLabel,
  supportsBondPrimary,
  supportsHashPolicy,
} from "./interface-fields";

/** Every control is a string while it's being typed; numbers are parsed on
 *  submit so a half-typed MTU doesn't collapse to 0. */
type FormKey = keyof NetworkInterfaceOptions;
type FormState = Partial<Record<FormKey, string>>;

const EMPTY_FORM: FormState = {};

interface InterfaceFormDialogProps {
  clusterId: string;
  nodeName: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The interface to edit. Omit to create a new one. */
  existing?: NetworkInterface;
}

export function InterfaceFormDialog({
  clusterId,
  nodeName,
  open,
  onOpenChange,
  existing,
}: InterfaceFormDialogProps) {
  const isEdit = existing !== undefined;

  const [iface, setIface] = useState("");
  const [type, setType] = useState("bridge");
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [autostart, setAutostart] = useState(true);
  const [vlanAware, setVlanAware] = useState(false);
  const [showAdvanced, setShowAdvanced] = useState(false);

  const create = useCreateNetworkInterface(clusterId, nodeName);
  const update = useUpdateNetworkInterface(clusterId, nodeName);
  const mutation = isEdit ? update : create;

  // Populate the OVS bridge picker from the node's existing bridges, the way
  // Proxmox's BridgeSelector does.
  const nodeInterfacesQuery = useNodeNetworkInterfaces(
    clusterId,
    open ? nodeName : "",
  );
  const ovsBridges = useMemo(
    () =>
      (nodeInterfacesQuery.data ?? [])
        .filter((i) => i.type === "OVSBridge")
        .map((i) => i.iface),
    [nodeInterfacesQuery.data],
  );

  // Reset to the interface under edit (or to create defaults) each time the
  // dialog opens, so a cancelled edit never leaks into the next one.
  useEffect(() => {
    if (!open) return;
    mutation.reset();
    if (existing) {
      setIface(existing.iface);
      setType(existing.type);
      setAutostart(existing.autostart === 1);
      setVlanAware(existing.bridge_vlan_aware === 1);
      setForm(toFormState(existing));
    } else {
      setIface("");
      setType("bridge");
      setAutostart(true);
      setVlanAware(false);
      // "bridge" has no bond mode; changeType seeds one when the type changes.
      setForm(EMPTY_FORM);
    }
    setShowAdvanced(false);
    // Keyed on the interface *name*, not the object: the row this dialog was
    // opened from is re-created by every background refetch of the interface
    // list, and depending on its identity would wipe what the operator has
    // typed. `mutation` is likewise a new object each render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, existing?.iface]);

  const fields = fieldsForType(type);
  const bondMode = form.bond_mode;
  const errorMessage =
    mutation.error instanceof Error ? mutation.error.message : "";

  const set = (key: FormKey) => (value: string) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  // Linux and OVS bonds share the bond_mode key but accept disjoint enums, so
  // a mode carried across a type change would submit a value the new type
  // rejects. Reset it to the type's own default, as Proxmox preselects.
  const changeType = (next: string) => {
    setType(next);
    setForm((prev) => {
      if (!fieldsForType(next).bondMode) return prev;
      const current = prev.bond_mode;
      if (current !== undefined && bondModesFor(next).includes(current)) {
        return prev;
      }
      return { ...prev, bond_mode: defaultBondMode(next) };
    });
  };

  // Proxmox marks these allowBlank:false; without the guard the request goes
  // out incomplete and comes back as a 500 rather than a form error.
  const missingRequired =
    iface.trim() === "" ||
    (fields.ovsBridge && (form.ovs_bridge ?? "") === "") ||
    (fields.bondMode && (form.bond_mode ?? "") === "");

  const handleSubmit = () => {
    const options = toOptions(form, fields, {
      // The OVS-attached types show no autostart checkbox, but the parameter
      // is always sent — so on edit the interface's own value has to be
      // carried through, or saving an unrelated field would drop its auto
      // line. On create those types default to off, as Proxmox leaves them.
      autostart: fields.autostart || isEdit ? autostart : false,
      vlanAware,
    });

    if (isEdit) {
      const req: UpdateNetworkInterfaceRequest = { type, ...options };
      const cleared = clearedSettings(existing, options, type);
      if (cleared.length > 0) req.delete = cleared;
      update.mutate(
        { iface: existing.iface, params: req },
        {
          onSuccess: () => {
            onOpenChange(false);
          },
        },
      );
      return;
    }

    const req: CreateNetworkInterfaceRequest = { iface, type, ...options };
    create.mutate(req, {
      onSuccess: () => {
        onOpenChange(false);
      },
    });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>
            {isEdit
              ? `Edit ${interfaceTypeLabel(type)}: ${existing.iface}`
              : `Create ${interfaceTypeLabel(type)} on ${nodeName}`}
          </DialogTitle>
        </DialogHeader>

        <div className="grid gap-x-6 gap-y-4 sm:grid-cols-2">
          {/* --- Identity and addressing --- */}
          <Field label="Name">
            <Input
              placeholder={namePlaceholder(type)}
              value={iface}
              disabled={isEdit}
              onChange={(e) => {
                setIface(e.target.value);
              }}
            />
          </Field>

          <Field label="Type">
            {isEdit ? (
              <Input value={interfaceTypeLabel(type)} disabled />
            ) : (
              <Select value={type} onValueChange={changeType}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {CREATABLE_INTERFACE_TYPES.map((t) => (
                    <SelectItem key={t.value} value={t.value}>
                      {t.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </Field>

          {fields.ip && (
            <>
              <Field label="IPv4/CIDR">
                <Input
                  placeholder="e.g. 10.0.0.2/24"
                  value={form.cidr ?? ""}
                  onChange={(e) => {
                    set("cidr")(e.target.value);
                  }}
                />
              </Field>
              <Field label="Gateway (IPv4)">
                <Input
                  placeholder="e.g. 10.0.0.1"
                  value={form.gateway ?? ""}
                  onChange={(e) => {
                    set("gateway")(e.target.value);
                  }}
                />
              </Field>
              <Field label="IPv6/CIDR">
                <Input
                  placeholder="e.g. fd00::2/64"
                  value={form.cidr6 ?? ""}
                  onChange={(e) => {
                    set("cidr6")(e.target.value);
                  }}
                />
              </Field>
              <Field label="Gateway (IPv6)">
                <Input
                  placeholder="e.g. fd00::1"
                  value={form.gateway6 ?? ""}
                  onChange={(e) => {
                    set("gateway6")(e.target.value);
                  }}
                />
              </Field>
            </>
          )}

          {/* --- Type-specific --- */}
          {fields.bridgePorts && (
            <Field
              label="Bridge ports"
              hint="Space-separated, e.g. “eno1 eno2”"
            >
              <Input
                placeholder="e.g. eno1"
                value={form.bridge_ports ?? ""}
                onChange={(e) => {
                  set("bridge_ports")(e.target.value);
                }}
              />
            </Field>
          )}

          {fields.slaves && (
            <Field label="Slaves" hint="Space-separated, e.g. “eno1 eno2”">
              <Input
                placeholder="e.g. eno1 eno2"
                value={form.slaves ?? ""}
                onChange={(e) => {
                  set("slaves")(e.target.value);
                }}
              />
            </Field>
          )}

          {fields.ovsBonds && (
            <Field label="Slaves" hint="Space-separated, e.g. “eno1 eno2”">
              <Input
                placeholder="e.g. eno1 eno2"
                value={form.ovs_bonds ?? ""}
                onChange={(e) => {
                  set("ovs_bonds")(e.target.value);
                }}
              />
            </Field>
          )}

          {fields.bondMode && (
            <Field label="Mode">
              <Select
                value={form.bond_mode ?? ""}
                onValueChange={(v) => {
                  setForm((prev) => {
                    const next = { ...prev, bond_mode: v };
                    // Both of these are meaningless outside the mode that
                    // enables them, and Proxmox rejects them there.
                    if (!supportsHashPolicy(v)) next.bond_xmit_hash_policy = "";
                    if (!supportsBondPrimary(v)) next["bond-primary"] = "";
                    return next;
                  });
                }}
              >
                <SelectTrigger>
                  <SelectValue placeholder="Select mode" />
                </SelectTrigger>
                <SelectContent>
                  {bondModesFor(type).map((m) => (
                    <SelectItem key={m} value={m}>
                      {m}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          )}

          {fields.bondMode && type === "bond" && (
            <>
              <Field
                label="Hash policy"
                hint={
                  supportsHashPolicy(bondMode)
                    ? undefined
                    : "Only for balance-xor and 802.3ad"
                }
              >
                <Select
                  value={form.bond_xmit_hash_policy ?? ""}
                  onValueChange={set("bond_xmit_hash_policy")}
                  disabled={!supportsHashPolicy(bondMode)}
                >
                  <SelectTrigger>
                    <SelectValue placeholder="—" />
                  </SelectTrigger>
                  <SelectContent>
                    {BOND_HASH_POLICIES.map((p) => (
                      <SelectItem key={p} value={p}>
                        {p}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>

              <Field
                label="bond-primary"
                hint={
                  supportsBondPrimary(bondMode)
                    ? undefined
                    : "Only for active-backup"
                }
              >
                <Input
                  placeholder="e.g. eno1"
                  value={form["bond-primary"] ?? ""}
                  disabled={!supportsBondPrimary(bondMode)}
                  onChange={(e) => {
                    set("bond-primary")(e.target.value);
                  }}
                />
              </Field>
            </>
          )}

          {fields.ovsBridge && (
            <Field label="OVS Bridge">
              {ovsBridges.length > 0 ? (
                <Select
                  value={form.ovs_bridge ?? ""}
                  onValueChange={set("ovs_bridge")}
                >
                  <SelectTrigger>
                    <SelectValue placeholder="Select bridge" />
                  </SelectTrigger>
                  <SelectContent>
                    {ovsBridges.map((b) => (
                      <SelectItem key={b} value={b}>
                        {b}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <>
                  {/* Typing the name is the right fallback for a node that
                      genuinely has no OVS bridge yet. It is also what a failed
                      read leaves behind, hence the note. */}
                  <Input
                    placeholder="e.g. vmbr1"
                    value={form.ovs_bridge ?? ""}
                    onChange={(e) => {
                      set("ovs_bridge")(e.target.value);
                    }}
                  />
                  <QueryFailureNote
                    query={nodeInterfacesQuery}
                    subject="this node's existing bridges"
                    identity={nodeName}
                    className="mt-1"
                  />
                </>
              )}
            </Field>
          )}

          {fields.ovsPorts && (
            <Field
              label="Bridge ports"
              hint="Space-separated, e.g. “eno1 eno2”"
            >
              <Input
                placeholder="e.g. eno1"
                value={form.ovs_ports ?? ""}
                onChange={(e) => {
                  set("ovs_ports")(e.target.value);
                }}
              />
            </Field>
          )}

          {fields.ovsTag && (
            <Field label="VLAN Tag" hint="1–4094, blank for none">
              <Input
                inputMode="numeric"
                placeholder="e.g. 100"
                value={form.ovs_tag ?? ""}
                onChange={(e) => {
                  set("ovs_tag")(e.target.value);
                }}
              />
            </Field>
          )}

          {fields.ovsOptions && (
            <Field label="OVS options">
              <Input
                placeholder="e.g. tag=100"
                value={form.ovs_options ?? ""}
                onChange={(e) => {
                  set("ovs_options")(e.target.value);
                }}
              />
            </Field>
          )}

          {fields.vlanRawDevice && (
            <Field
              label="VLAN raw device"
              hint="Leave blank when the name is like “eno1.100”"
            >
              <Input
                placeholder="e.g. eno1"
                value={form["vlan-raw-device"] ?? ""}
                onChange={(e) => {
                  set("vlan-raw-device")(e.target.value);
                }}
              />
            </Field>
          )}

          {fields.vlanId && (
            <Field label="VLAN Tag" hint="1–4094">
              <Input
                inputMode="numeric"
                placeholder="e.g. 100"
                value={form["vlan-id"] ?? ""}
                onChange={(e) => {
                  set("vlan-id")(e.target.value);
                }}
              />
            </Field>
          )}

          <Field label="Comment">
            <Input
              value={form.comments ?? ""}
              onChange={(e) => {
                set("comments")(e.target.value);
              }}
            />
          </Field>

          {/* --- Checkboxes --- */}
          <div className="flex items-center gap-6 sm:col-span-2">
            {fields.autostart && (
              <div className="flex items-center gap-2">
                <Checkbox
                  id="iface-autostart"
                  checked={autostart}
                  onCheckedChange={(v) => {
                    setAutostart(v === true);
                  }}
                />
                <Label htmlFor="iface-autostart">Autostart</Label>
              </div>
            )}
            {fields.vlanAware && (
              <div className="flex items-center gap-2">
                <Checkbox
                  id="iface-vlan-aware"
                  checked={vlanAware}
                  onCheckedChange={(v) => {
                    setVlanAware(v === true);
                  }}
                />
                <Label htmlFor="iface-vlan-aware">VLAN aware</Label>
              </div>
            )}
          </div>

          {/* --- Advanced --- */}
          <div className="sm:col-span-2">
            <button
              type="button"
              className="text-sm text-muted-foreground underline-offset-4 hover:underline"
              onClick={() => {
                setShowAdvanced((v) => !v);
              }}
            >
              {showAdvanced ? "Hide advanced" : "Show advanced"}
            </button>
          </div>

          {showAdvanced && (
            <>
              <Field label="MTU" hint="1280–65520, blank for the default">
                <Input
                  inputMode="numeric"
                  placeholder="1500"
                  value={form.mtu ?? ""}
                  onChange={(e) => {
                    set("mtu")(e.target.value);
                  }}
                />
              </Field>
              {fields.vlanAware && vlanAware && (
                <Field label="VLAN IDs" hint="e.g. “2 4 100-200”">
                  <Input
                    placeholder="2 4 100-200"
                    value={form.bridge_vids ?? ""}
                    onChange={(e) => {
                      set("bridge_vids")(e.target.value);
                    }}
                  />
                </Field>
              )}
            </>
          )}
        </div>

        {errorMessage && (
          <p className="text-sm text-destructive">{errorMessage}</p>
        )}

        <p className="text-xs text-muted-foreground">
          Changes are staged as a pending configuration. Use Apply Config to
          activate them, or Revert to discard.
        </p>

        <div className="flex justify-end gap-2">
          <Button
            variant="outline"
            onClick={() => {
              onOpenChange(false);
            }}
          >
            Cancel
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={missingRequired || mutation.isPending}
          >
            {mutation.isPending
              ? isEdit
                ? "Saving…"
                : "Creating…"
              : isEdit
                ? "Save"
                : "Create"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string | undefined;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-2">
      <Label>{label}</Label>
      {children}
      {hint !== undefined && (
        <p className="text-xs text-muted-foreground">{hint}</p>
      )}
    </div>
  );
}

function namePlaceholder(type: string): string {
  const match = CREATABLE_INTERFACE_TYPES.find((t) => t.value === type);
  if (match && match.namePrefix !== "") return `e.g. ${match.namePrefix}1`;
  if (type === "vlan") return "e.g. eno1.100";
  return "e.g. ovsint0";
}

/** Existing interface → editable string fields. */
function toFormState(existing: NetworkInterface): FormState {
  const state: FormState = {};
  const put = (key: FormKey, value: string | number | undefined) => {
    if (value === undefined || value === "" || value === 0) return;
    state[key] = String(value);
  };
  // Only the CIDR forms: `address` carries no prefix length, and Proxmox
  // validates the cidr parameter as ipv4/ipv6-with-prefix, so prefilling from
  // it would submit a value the node rejects.
  put("cidr", existing.cidr);
  put("gateway", existing.gateway);
  put("cidr6", existing.cidr6);
  put("gateway6", existing.gateway6);
  put("comments", existing.comments);
  put("mtu", existing.mtu);
  put("bridge_ports", existing.bridge_ports);
  put("bridge_vids", existing.bridge_vids);
  put("slaves", existing.slaves);
  put("bond_mode", existing.bond_mode);
  put("bond_xmit_hash_policy", existing.bond_xmit_hash_policy);
  put("bond-primary", existing["bond-primary"]);
  put("ovs_bridge", existing.ovs_bridge);
  put("ovs_ports", existing.ovs_ports);
  put("ovs_bonds", existing.ovs_bonds);
  put("ovs_options", existing.ovs_options);
  put("ovs_tag", existing.ovs_tag);
  put("vlan-id", existing["vlan-id"]);
  put("vlan-raw-device", existing["vlan-raw-device"]);
  return state;
}

/** Form strings → the request body, dropping anything the type doesn't use so
 *  a field left over from a previous type selection is never submitted. */
function toOptions(
  form: FormState,
  fields: ReturnType<typeof fieldsForType>,
  flags: { autostart: boolean; vlanAware: boolean },
): NetworkInterfaceOptions {
  const options: NetworkInterfaceOptions = {};
  const text = (key: keyof NetworkInterfaceOptions, enabled: boolean) => {
    const value = form[key]?.trim();
    if (enabled && value !== undefined && value !== "") {
      (options[key] as string) = value;
    }
  };
  const number = (key: keyof NetworkInterfaceOptions, enabled: boolean) => {
    const value = form[key]?.trim();
    if (!enabled || value === undefined || value === "") return;
    const parsed = Number(value);
    if (Number.isFinite(parsed)) (options[key] as number) = parsed;
  };

  text("cidr", fields.ip);
  text("gateway", fields.ip);
  text("cidr6", fields.ip);
  text("gateway6", fields.ip);
  text("comments", true);
  number("mtu", true);
  text("bridge_ports", fields.bridgePorts);
  text("bridge_vids", fields.vlanAware && flags.vlanAware);
  text("slaves", fields.slaves);
  text("bond_mode", fields.bondMode);
  text(
    "bond_xmit_hash_policy",
    fields.bondMode && supportsHashPolicy(form.bond_mode),
  );
  text("bond-primary", fields.bondMode && supportsBondPrimary(form.bond_mode));
  text("ovs_bridge", fields.ovsBridge);
  text("ovs_ports", fields.ovsPorts);
  text("ovs_bonds", fields.ovsBonds);
  text("ovs_options", fields.ovsOptions);
  number("ovs_tag", fields.ovsTag);
  number("vlan-id", fields.vlanId);
  text("vlan-raw-device", fields.vlanRawDevice);

  // Always sent: the form builder writes autostart unconditionally, so leaving
  // it out of the body would bind to 0 and silently disable it.
  options.autostart = flags.autostart ? 1 : 0;
  // Off is expressed by omission; clearing it on an existing bridge is
  // clearedSettings' job, matching how Proxmox's own dialog does it.
  if (fields.vlanAware && flags.vlanAware) options.bridge_vlan_aware = 1;

  return options;
}
