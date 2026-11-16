import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import { Segmented } from "@/components/ui/segmented";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { EmptyState } from "@/components/EmptyState";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { SettingRow, SettingsSection } from "@/components/page/Settings";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  BookUser,
  Plus,
  Trash2,
  RefreshCw,
  Plug,
  Loader2,
  CheckCircle2,
  XCircle,
} from "lucide-react";
import { AdminNav } from "../components/AdminNav";
import {
  useLDAPConfigs,
  useCreateLDAPConfig,
  useUpdateLDAPConfig,
  useDeleteLDAPConfig,
  useTestLDAPConnection,
  useSyncLDAP,
} from "../api/ldap-queries";
import { useRoles } from "../api/rbac-queries";
import type { LDAPConfig, LDAPConfigRequest } from "@/types/api";
import {
  confirmRequiredFromError,
  confirmDetailString,
  type ConfirmRequired,
} from "@/lib/confirm-gate";
import { ConfirmRequiredWarning } from "@/components/ConfirmRequiredWarning";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { describeError } from "@/lib/api-error";
import { DateTime } from "@/components/DateTime";

/** The backend's code for an LDAP transport downgrade (ldap.go). */
const INSECURE_LDAP_TRANSPORT = "insecure_ldap_transport_confirm_required";

/** The backend's code for a sync the mass-miss rule stopped (ldap.go). */
const SYNC_TOO_MANY_MISSING = "ldap_sync_too_many_missing";

type DirectoryType = "openldap" | "ad";

/**
 * A setting whose control runs the full width under its label: a DN or a
 * search filter does not fit the narrow control column.
 */
const WIDE = "sm:grid-cols-1";

const presets: Record<
  DirectoryType,
  { label: string; defaults: Partial<LDAPConfigRequest> }
> = {
  openldap: {
    label: "OpenLDAP",
    defaults: {
      user_filter: "(|(uid={{username}})(mail={{username}}))",
      username_attribute: "uid",
      email_attribute: "mail",
      display_name_attribute: "cn",
      group_filter: "(member={{userDN}})",
      group_attribute: "cn",
    },
  },
  ad: {
    label: "Active Directory",
    defaults: {
      user_filter:
        "(|(sAMAccountName={{username}})(userPrincipalName={{username}})(mail={{username}}))",
      username_attribute: "sAMAccountName",
      email_attribute: "mail",
      display_name_attribute: "displayName",
      group_filter: "(member={{userDN}})",
      group_attribute: "cn",
    },
  },
};

const emptyForm: LDAPConfigRequest = {
  name: "Default",
  enabled: false,
  server_url: "",
  start_tls: false,
  skip_tls_verify: false,
  bind_dn: "",
  bind_password: "",
  search_base_dn: "",
  user_filter: "(|(uid={{username}})(mail={{username}}))",
  username_attribute: "uid",
  email_attribute: "mail",
  display_name_attribute: "cn",
  group_search_base_dn: "",
  group_filter: "(member={{userDN}})",
  group_attribute: "cn",
  group_role_mapping: {},
  default_role_id: null,
  sync_interval_minutes: 60,
};

function detectDirectoryType(cfg: LDAPConfigRequest): DirectoryType {
  if (
    cfg.username_attribute === "sAMAccountName" ||
    cfg.user_filter.includes("sAMAccountName")
  ) {
    return "ad";
  }
  return "openldap";
}

/** The number of missing accounts a stopped sync reports. */
function missingCount(stopped: ConfirmRequired): number {
  const n = stopped.details["users_missing"];
  return typeof n === "number" ? n : 0;
}

function accountsLabel(n: number): string {
  return `${String(n)} account${n === 1 ? "" : "s"}`;
}

function configToForm(cfg: LDAPConfig): LDAPConfigRequest {
  return {
    name: cfg.name,
    enabled: cfg.enabled,
    server_url: cfg.server_url,
    start_tls: cfg.start_tls,
    skip_tls_verify: cfg.skip_tls_verify,
    bind_dn: cfg.bind_dn,
    bind_password: "",
    search_base_dn: cfg.search_base_dn,
    user_filter: cfg.user_filter,
    username_attribute: cfg.username_attribute,
    email_attribute: cfg.email_attribute,
    display_name_attribute: cfg.display_name_attribute,
    group_search_base_dn: cfg.group_search_base_dn,
    group_filter: cfg.group_filter,
    group_attribute: cfg.group_attribute,
    group_role_mapping: cfg.group_role_mapping,
    default_role_id: cfg.default_role_id,
    sync_interval_minutes: cfg.sync_interval_minutes,
  };
}

export function LDAPPage() {
  const { data: configs, isLoading } = useLDAPConfigs();
  const { data: roles } = useRoles();
  const createConfig = useCreateLDAPConfig();
  const updateConfig = useUpdateLDAPConfig();
  const deleteConfig = useDeleteLDAPConfig();
  const testConnection = useTestLDAPConnection();
  const syncLDAP = useSyncLDAP();

  const [editingId, setEditingId] = useState<string | null>(null);
  const [form, setForm] = useState<LDAPConfigRequest>(emptyForm);
  const [isNew, setIsNew] = useState(false);
  const [directoryType, setDirectoryType] = useState<DirectoryType>("openldap");
  const [testUsername, setTestUsername] = useState("");
  const [testResult, setTestResult] = useState<{
    success: boolean;
    message: string;
  } | null>(null);
  const [syncResult, setSyncResult] = useState<string | null>(null);
  // A sync the mass-miss rule stopped, which the operator can repeat on purpose.
  const [syncWarning, setSyncWarning] = useState<ConfirmRequired | null>(null);

  // Group mapping rows for editor
  const [mappingRows, setMappingRows] = useState<
    Array<{ groupDN: string; roleId: string }>
  >([]);

  const activeConfig = configs?.find((c) => c.id === editingId);

  // The stored bind password is only ever sent to the directory it was saved
  // for, so moving the server URL means re-entering it. The backend refuses
  // the combination outright (LDAPHandler.Update). A config that binds
  // anonymously has no stored password and is not affected.
  // Confirm gate for a transport downgrade (cleartext, or TLS with no
  // certificate check). The backend answers the first attempt with a 422
  // rather than saving; accepting re-submits with acknowledge_insecure_tls.
  const [transportWarning, setTransportWarning] =
    useState<ConfirmRequired | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);

  const addressChanged =
    !isNew &&
    activeConfig != null &&
    activeConfig.bind_password_set &&
    form.server_url !== activeConfig.server_url;

  useEffect(() => {
    // Skip while a confirm is pending: re-seeding would replace the values the
    // operator is being asked about, so the confirm button would then submit
    // the config unchanged — silently discarding their edit while reporting
    // success.
    if (transportWarning != null) return;
    if (activeConfig) {
      const f = configToForm(activeConfig);
      setForm(f);
      setDirectoryType(detectDirectoryType(f));
      setMappingRows(
        Object.entries(activeConfig.group_role_mapping).map(
          ([groupDN, roleId]) => ({ groupDN, roleId }),
        ),
      );
    }
    // transportWarning is read, not depended on, and deliberately absent from the
    // deps: including it would re-run this the moment the confirm clears,
    // re-seeding the form from the server and discarding the edit the operator
    // had just been asked to confirm.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeConfig]);

  // Clears the per-attempt feedback. The transport confirm in particular MUST
  // be cleared when the form switches configs: left mounted, its confirm button
  // would submit whichever config is open now, carrying an acknowledgement the
  // operator gave for a different directory.
  const clearFeedback = () => {
    setTestResult(null);
    setSyncResult(null);
    setSyncWarning(null);
    setTransportWarning(null);
    setSaveError(null);
  };

  const startNew = () => {
    setForm(emptyForm);
    setMappingRows([]);
    setIsNew(true);
    setEditingId(null);
    clearFeedback();
  };

  const startEdit = (cfg: LDAPConfig) => {
    setEditingId(cfg.id);
    setIsNew(false);
    clearFeedback();
  };

  const cancel = () => {
    setEditingId(null);
    setIsNew(false);
    clearFeedback();
  };

  const buildMappingFromRows = () => {
    const mapping: Record<string, string> = {};
    for (const row of mappingRows) {
      if (row.groupDN && row.roleId) {
        mapping[row.groupDN] = row.roleId;
      }
    }
    return mapping;
  };

  // Which config a save belongs to, so a mutation that settles after the
  // operator has switched configs cannot leave its prompt on a different one.
  const saveTarget = isNew ? null : editingId;

  // Both hooks opt out of the global error toast, because the open form shows
  // its failure itself; the save settles through its promise, so that one that
  // settles after the form has gone is toasted instead of lost (see
  // useSaveOutcome). Cancel does not wait for a save, and the page opens one
  // form after another, so a form is told apart by what it is: the new one, or
  // the id of the config being edited (null when there is none).
  const settle = useSaveOutcome(isNew ? "new" : editingId);

  const save = (acknowledgeInsecureTLS: boolean) => {
    const data: LDAPConfigRequest = {
      ...form,
      group_role_mapping: buildMappingFromRows(),
      ...(acknowledgeInsecureTLS ? { acknowledge_insecure_tls: true } : {}),
    };
    const target = saveTarget;
    // What a failure nobody is looking at names: the config as the operator
    // knows it, which is the stored name when an edit renames it.
    const stored = isNew ? undefined : activeConfig?.name;
    const named = `"${stored ?? data.name}"`;

    // The warning stays mounted across a confirmed re-submit so its button can
    // show progress; it is cleared on success, or replaced on a fresh refusal.
    setSaveError(null);
    if (!acknowledgeInsecureTLS) {
      setTransportWarning(null);
    }

    const onSettledSuccess = () => {
      setTransportWarning(null);
    };

    const onError = (err: unknown) => {
      const confirm = confirmRequiredFromError(
        err,
        [INSECURE_LDAP_TRANSPORT],
        target,
      );
      if (confirm != null) {
        setTransportWarning(confirm);
        return;
      }
      setTransportWarning(null);
      setSaveError(
        err instanceof Error
          ? err.message
          : "Failed to save LDAP configuration",
      );
    };

    // Not once the form has been left: either success would close, and clear
    // the prompt of, whichever form has been opened in its place.
    if (isNew) {
      settle(createConfig.mutateAsync(data), {
        action: `Creating the LDAP configuration ${named}`,
        onSuccess: () => {
          setIsNew(false);
          onSettledSuccess();
        },
        onError,
      });
    } else if (editingId) {
      settle(updateConfig.mutateAsync({ ...data, id: editingId }), {
        action: `Saving the LDAP configuration ${named}`,
        onSuccess: () => {
          setEditingId(null);
          onSettledSuccess();
        },
        onError,
      });
    }
  };

  const handleSave = () => {
    save(false);
  };

  const handleTest = () => {
    if (!editingId) return;
    setTestResult(null);
    const testPayload: { id: string; test_username?: string } = {
      id: editingId,
    };
    if (testUsername) {
      testPayload.test_username = testUsername;
    }
    settle(testConnection.mutateAsync(testPayload), {
      action: `Testing the LDAP configuration "${activeConfig?.name ?? form.name}"`,
      onSuccess: (result) => {
        setTestResult(result);
      },
      onError: (err) => {
        setTestResult({
          success: false,
          message: describeError(err) || "Request failed",
        });
      },
    });
  };

  const handleSync = (acknowledgeMissing?: number) => {
    if (!editingId) return;
    const target = editingId;
    setSyncResult(null);
    if (acknowledgeMissing == null) {
      setSyncWarning(null);
    }
    settle(
      syncLDAP.mutateAsync({
        id: target,
        acknowledge_missing: acknowledgeMissing,
      }),
      {
        action: `Syncing the LDAP configuration "${activeConfig?.name ?? form.name}"`,
        onSuccess: (result) => {
          const parts = [`${String(result.users_synced)} synced`];
          if (result.users_disabled > 0) {
            parts.push(`${String(result.users_disabled)} disabled`);
          }
          if (result.users_re_enabled > 0) {
            parts.push(`${String(result.users_re_enabled)} re-enabled`);
          }
          if ((result.users_kept_active ?? 0) > 0) {
            parts.push(
              `${String(result.users_kept_active)} kept active (you, or the last user manager)`,
            );
          }
          setSyncResult(`${result.message} (${parts.join(", ")})`);
          setSyncWarning(null);
        },
        onError: (err) => {
          const stopped = confirmRequiredFromError(
            err,
            [SYNC_TOO_MANY_MISSING],
            target,
          );
          if (stopped != null && missingCount(stopped) > 0) {
            setSyncWarning(stopped);
            return;
          }
          setSyncWarning(null);
          const reason = describeError(err);
          setSyncResult(reason ? `Sync failed: ${reason}` : "Sync failed");
        },
      },
    );
  };

  const showForm = isNew || editingId;

  if (isLoading) {
    return (
      <div>
        <AdminNav />
        <div className="flex h-64 items-center justify-center text-muted-foreground">
          Loading LDAP configuration...
        </div>
      </div>
    );
  }

  const total = configs?.length ?? 0;
  const enabledCount = configs?.filter((c) => c.enabled).length ?? 0;
  const saving = createConfig.isPending || updateConfig.isPending;
  const applyPreset = (key: DirectoryType) => {
    setDirectoryType(key);
    setForm((prev) => ({ ...prev, ...presets[key].defaults }));
  };

  return (
    <div>
      <AdminNav />
      <div className="space-y-5">
        <PageHeader
          title="LDAP / Active Directory"
          summary={
            total === 0
              ? "No directory is configured, so nobody signs in through LDAP."
              : `${String(total)} director${total !== 1 ? "ies" : "y"} configured, ${String(enabledCount)} enabled. Group membership decides each person's role.`
          }
          actions={
            !showForm ? (
              <Button onClick={startNew}>
                <Plus className="h-4 w-4" />
                Add configuration
              </Button>
            ) : undefined
          }
        />

        {/* Config list */}
        {!showForm && configs && configs.length > 0 && (
          <Panel flush>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Server</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="whitespace-nowrap">Last sync</TableHead>
                  <TableHead className="w-32">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {configs.map((cfg) => (
                  <TableRow key={cfg.id}>
                    <TableCell className="whitespace-nowrap font-medium">
                      {cfg.name}
                    </TableCell>
                    <TableCell className="min-w-[14rem] break-all font-mono text-[13px] text-muted-foreground">
                      {cfg.server_url}
                    </TableCell>
                    <TableCell>
                      {cfg.enabled ? (
                        <Badge variant="healthy">Enabled</Badge>
                      ) : (
                        <span className="text-muted-foreground">Disabled</span>
                      )}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      <DateTime value={cfg.last_sync_at} fallback="Never" />
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => {
                            startEdit(cfg);
                          }}
                        >
                          Edit
                        </Button>
                        <Button
                          aria-label={`Delete ${cfg.name}`}
                          variant="ghost"
                          size="icon"
                          className="h-8 w-8 text-muted-foreground hover:text-destructive"
                          onClick={() => {
                            if (
                              confirm(
                                "Delete this LDAP configuration? LDAP users will no longer be able to log in.",
                              )
                            ) {
                              deleteConfig.mutate(cfg.id);
                            }
                          }}
                        >
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Panel>
        )}

        {!showForm && (!configs || configs.length === 0) && (
          <EmptyState
            icon={BookUser}
            title="No LDAP configuration"
            description="Add a configuration to let people sign in with their OpenLDAP or Active Directory account."
          />
        )}

        {/* Edit/Create form */}
        {showForm && (
          <div className="max-w-3xl space-y-4">
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
              <h2 className="text-lg font-semibold">
                {isNew ? "New LDAP configuration" : "Edit LDAP configuration"}
              </h2>
              {!isNew && activeConfig != null && (
                <span className="text-sm text-muted-foreground">
                  {activeConfig.name}
                </span>
              )}
            </div>

            <SettingsSection title="Directory">
              <SettingRow
                label="Directory type"
                description={
                  directoryType === "ad"
                    ? "Pre-filled for Active Directory (sAMAccountName, userPrincipalName, displayName)"
                    : "Pre-filled for OpenLDAP (uid, mail, cn)"
                }
              >
                <Segmented
                  aria-label="Directory type"
                  size="md"
                  value={directoryType}
                  onValueChange={applyPreset}
                  // Pressing the current type again re-applies its defaults.
                  onReselect={applyPreset}
                  options={(
                    Object.entries(presets) as Array<
                      [DirectoryType, (typeof presets)[DirectoryType]]
                    >
                  ).map(([key, preset]) => ({
                    value: key,
                    label: preset.label,
                  }))}
                />
              </SettingRow>
              <SettingRow
                label="Name"
                description="What this configuration is called in the list."
                htmlFor="ldap-name"
              >
                <Input
                  id="ldap-name"
                  value={form.name}
                  onChange={(e) => {
                    setForm({ ...form, name: e.target.value });
                  }}
                  placeholder="Default"
                />
              </SettingRow>
              <SettingRow
                label="Enabled"
                description="People can sign in through this directory only while it is on."
                htmlFor="ldap-enabled"
              >
                <Switch
                  id="ldap-enabled"
                  checked={form.enabled}
                  onCheckedChange={(enabled) => {
                    setForm({ ...form, enabled });
                  }}
                />
              </SettingRow>
            </SettingsSection>

            <SettingsSection title="Connection">
              <SettingRow
                className={WIDE}
                label="Server URL"
                description={
                  addressChanged
                    ? "This directory is moving to a new address, so it needs the bind password again. The stored one is only ever sent to the address it was saved for."
                    : "Starts with ldap:// or ldaps:// and names the port."
                }
                htmlFor="ldap-server-url"
              >
                <Input
                  id="ldap-server-url"
                  className="font-mono"
                  value={form.server_url}
                  onChange={(e) => {
                    setForm({ ...form, server_url: e.target.value });
                  }}
                  placeholder="ldap://ldap.example.com:389"
                />
              </SettingRow>
              <SettingRow
                label="StartTLS"
                description="Upgrade an ldap:// connection to TLS before any password is sent."
                htmlFor="ldap-start-tls"
              >
                <Switch
                  id="ldap-start-tls"
                  checked={form.start_tls}
                  onCheckedChange={(start_tls) => {
                    setForm({ ...form, start_tls });
                  }}
                />
              </SettingRow>
              <SettingRow
                label="Skip TLS verify"
                description="Accept the server's certificate without checking it."
                htmlFor="ldap-skip-tls-verify"
              >
                <Switch
                  id="ldap-skip-tls-verify"
                  checked={form.skip_tls_verify}
                  onCheckedChange={(skip_tls_verify) => {
                    setForm({ ...form, skip_tls_verify });
                  }}
                />
              </SettingRow>
              <SettingRow
                className={WIDE}
                label="Bind DN"
                description="The account Nexara searches the directory as."
                htmlFor="ldap-bind-dn"
              >
                <Input
                  id="ldap-bind-dn"
                  className="font-mono"
                  value={form.bind_dn}
                  onChange={(e) => {
                    setForm({ ...form, bind_dn: e.target.value });
                  }}
                  placeholder="cn=admin,dc=example,dc=com"
                />
              </SettingRow>
              <SettingRow
                label="Bind password"
                description={
                  !isNew && activeConfig?.bind_password_set
                    ? addressChanged
                      ? "Required: the address changed."
                      : "Leave blank to keep the current one."
                    : undefined
                }
                htmlFor="ldap-bind-password"
              >
                <Input
                  id="ldap-bind-password"
                  type="password"
                  value={form.bind_password}
                  onChange={(e) => {
                    setForm({ ...form, bind_password: e.target.value });
                  }}
                  placeholder={
                    addressChanged
                      ? "Re-enter the bind password for the new address"
                      : !isNew && activeConfig?.bind_password_set
                        ? "********"
                        : "Password"
                  }
                  required={addressChanged}
                />
              </SettingRow>
            </SettingsSection>

            <SettingsSection
              title="User search"
              description="How a person who signs in is found, and which attributes become their account."
            >
              <SettingRow
                className={WIDE}
                label="Search base DN"
                htmlFor="ldap-search-base-dn"
              >
                <Input
                  id="ldap-search-base-dn"
                  className="font-mono"
                  value={form.search_base_dn}
                  onChange={(e) => {
                    setForm({ ...form, search_base_dn: e.target.value });
                  }}
                  placeholder="dc=example,dc=com"
                />
              </SettingRow>
              <SettingRow
                className={WIDE}
                label="User filter"
                description="{{username}} stands for what was typed at sign-in."
                htmlFor="ldap-user-filter"
              >
                <Input
                  id="ldap-user-filter"
                  className="font-mono"
                  value={form.user_filter}
                  onChange={(e) => {
                    setForm({ ...form, user_filter: e.target.value });
                  }}
                  placeholder="(|(uid={{username}})(mail={{username}}))"
                />
              </SettingRow>
              <SettingRow
                label="Username attribute"
                htmlFor="ldap-username-attribute"
              >
                <Input
                  id="ldap-username-attribute"
                  className="font-mono"
                  value={form.username_attribute}
                  onChange={(e) => {
                    setForm({ ...form, username_attribute: e.target.value });
                  }}
                  placeholder="uid"
                />
              </SettingRow>
              <SettingRow
                label="Email attribute"
                htmlFor="ldap-email-attribute"
              >
                <Input
                  id="ldap-email-attribute"
                  className="font-mono"
                  value={form.email_attribute}
                  onChange={(e) => {
                    setForm({ ...form, email_attribute: e.target.value });
                  }}
                  placeholder="mail"
                />
              </SettingRow>
              <SettingRow
                label="Display name attribute"
                htmlFor="ldap-display-name-attribute"
              >
                <Input
                  id="ldap-display-name-attribute"
                  className="font-mono"
                  value={form.display_name_attribute}
                  onChange={(e) => {
                    setForm({
                      ...form,
                      display_name_attribute: e.target.value,
                    });
                  }}
                  placeholder="cn"
                />
              </SettingRow>
            </SettingsSection>

            <SettingsSection title="Group search">
              <SettingRow
                className={WIDE}
                label="Group search base DN"
                htmlFor="ldap-group-search-base-dn"
              >
                <Input
                  id="ldap-group-search-base-dn"
                  className="font-mono"
                  value={form.group_search_base_dn}
                  onChange={(e) => {
                    setForm({
                      ...form,
                      group_search_base_dn: e.target.value,
                    });
                  }}
                  placeholder="ou=groups,dc=example,dc=com"
                />
              </SettingRow>
              <SettingRow
                className={WIDE}
                label="Group filter"
                description="{{userDN}} stands for the DN of the person signing in."
                htmlFor="ldap-group-filter"
              >
                <Input
                  id="ldap-group-filter"
                  className="font-mono"
                  value={form.group_filter}
                  onChange={(e) => {
                    setForm({ ...form, group_filter: e.target.value });
                  }}
                  placeholder="(member={{userDN}})"
                />
              </SettingRow>
              <SettingRow
                label="Group attribute"
                htmlFor="ldap-group-attribute"
              >
                <Input
                  id="ldap-group-attribute"
                  className="font-mono"
                  value={form.group_attribute}
                  onChange={(e) => {
                    setForm({ ...form, group_attribute: e.target.value });
                  }}
                  placeholder="cn"
                />
              </SettingRow>
            </SettingsSection>

            <SettingsSection
              title="Group-to-role mapping"
              description="Each mapped group gives its members a role."
              actions={
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    setMappingRows([
                      ...mappingRows,
                      { groupDN: "", roleId: "" },
                    ]);
                  }}
                >
                  <Plus className="h-3.5 w-3.5" />
                  Add mapping
                </Button>
              }
            >
              {mappingRows.length === 0 ? (
                <p className="pb-3.5 pt-1 text-sm text-muted-foreground">
                  No mappings configured. LDAP users will be assigned the
                  default role.
                </p>
              ) : (
                <div className="space-y-2 pb-3.5 pt-1">
                  {mappingRows.map((row, idx) => (
                    <div
                      key={idx}
                      className="flex flex-wrap items-center gap-2"
                    >
                      <Input
                        aria-label={`Group DN of mapping ${String(idx + 1)}`}
                        className="min-w-0 flex-[2_1_14rem] font-mono"
                        value={row.groupDN}
                        onChange={(e) => {
                          const updated = [...mappingRows];
                          updated[idx] = { ...row, groupDN: e.target.value };
                          setMappingRows(updated);
                        }}
                        placeholder="CN=Admins,OU=Groups,DC=example,DC=com"
                      />
                      <Select
                        value={row.roleId}
                        onValueChange={(val) => {
                          const updated = [...mappingRows];
                          updated[idx] = { ...row, roleId: val };
                          setMappingRows(updated);
                        }}
                      >
                        <SelectTrigger
                          aria-label={`Role of mapping ${String(idx + 1)}`}
                          className="min-w-0 flex-[1_1_10rem]"
                        >
                          <SelectValue placeholder="Select role" />
                        </SelectTrigger>
                        <SelectContent>
                          {roles?.map((role) => (
                            <SelectItem key={role.id} value={role.id}>
                              {role.name}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      <Button
                        aria-label={`Remove group mapping ${row.groupDN || String(idx + 1)}`}
                        variant="ghost"
                        size="icon"
                        className="text-muted-foreground hover:text-destructive"
                        onClick={() => {
                          setMappingRows(
                            mappingRows.filter((_, i) => i !== idx),
                          );
                        }}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                  ))}
                </div>
              )}
              <SettingRow
                label="Default role"
                description="Given to someone who is in no mapped group."
                htmlFor="ldap-default-role"
              >
                <Select
                  value={form.default_role_id ?? "none"}
                  onValueChange={(val) => {
                    setForm({
                      ...form,
                      default_role_id: val === "none" ? null : val,
                    });
                  }}
                >
                  <SelectTrigger
                    id="ldap-default-role"
                    className="w-full sm:w-64"
                  >
                    <SelectValue placeholder="No default role" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="none">No default role</SelectItem>
                    {roles?.map((role) => (
                      <SelectItem key={role.id} value={role.id}>
                        {role.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </SettingRow>
            </SettingsSection>

            <SettingsSection title="Sync">
              <SettingRow
                label="Sync interval"
                description="Minutes between background syncs of this directory. 0 turns them off."
                htmlFor="ldap-sync-interval"
              >
                <Input
                  id="ldap-sync-interval"
                  className="w-24"
                  type="number"
                  min={0}
                  value={form.sync_interval_minutes}
                  onChange={(e) => {
                    const minutes = parseInt(e.target.value, 10);
                    setForm({
                      ...form,
                      sync_interval_minutes: Number.isNaN(minutes) ? 60 : minutes,
                    });
                  }}
                />
                <span className="text-sm text-muted-foreground">minutes</span>
              </SettingRow>
              {!isNew && editingId && (
                <SettingRow
                  label="Sync now"
                  description={
                    syncResult ??
                    "Runs a sync of the saved configuration without waiting for the interval."
                  }
                >
                  <Button
                    variant="outline"
                    onClick={() => {
                      handleSync();
                    }}
                    disabled={syncLDAP.isPending}
                  >
                    {syncLDAP.isPending ? (
                      <Loader2 className="h-4 w-4 animate-spin" />
                    ) : (
                      <RefreshCw className="h-4 w-4" />
                    )}
                    Sync now
                  </Button>
                </SettingRow>
              )}
              {syncWarning != null && syncWarning.target === editingId && (
                <div className="pb-3.5">
                  <ConfirmRequiredWarning
                    title={`Deactivate ${accountsLabel(missingCount(syncWarning))}?`}
                    message={`The directory found no entry for ${accountsLabel(missingCount(syncWarning))}, so the sync changed nothing. This usually means the user filter, the search base DN or the bind account's access is wrong. Deactivate them only if these people have left: the accounts named will no longer be able to sign in, and their sessions end. Your own account and the last user manager stay active.`}
                    confirmLabel={`Deactivate ${accountsLabel(missingCount(syncWarning))}`}
                    onConfirm={() => {
                      handleSync(missingCount(syncWarning));
                    }}
                    onCancel={() => {
                      setSyncWarning(null);
                    }}
                    pending={syncLDAP.isPending}
                  />
                </div>
              )}
            </SettingsSection>

            {/* Test connection: the saved configuration, not the edits above */}
            {!isNew && editingId && (
              <SettingsSection
                title="Test connection"
                description="Tries the configuration as it is saved. Edits above are not tested until they are saved."
              >
                <SettingRow
                  label="Test username"
                  description="Optional."
                  htmlFor="ldap-test-username"
                >
                  <Input
                    id="ldap-test-username"
                    className="min-w-0 flex-1"
                    value={testUsername}
                    onChange={(e) => {
                      setTestUsername(e.target.value);
                    }}
                    placeholder="Test username (optional)"
                  />
                  <Button
                    variant="outline"
                    onClick={handleTest}
                    disabled={testConnection.isPending}
                  >
                    {testConnection.isPending ? (
                      <Loader2 className="h-4 w-4 animate-spin" />
                    ) : (
                      <Plug className="h-4 w-4" />
                    )}
                    Test
                  </Button>
                </SettingRow>
                {testResult && (
                  <div className="flex items-start gap-2 pt-3.5 text-sm">
                    {testResult.success ? (
                      <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-emerald-600 dark:text-emerald-500" />
                    ) : (
                      <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
                    )}
                    <span
                      className={
                        testResult.success ? undefined : "text-destructive"
                      }
                    >
                      {testResult.message}
                    </span>
                  </div>
                )}
              </SettingsSection>
            )}

            {transportWarning != null &&
              transportWarning.target === saveTarget && (
                <ConfirmRequiredWarning
                  title={
                    confirmDetailString(transportWarning, "transport_kind") ===
                    "cleartext"
                      ? "Passwords would travel in cleartext"
                      : "Certificate would never be verified"
                  }
                  message={`${transportWarning.message} This is fine for a self-hosted lab directory — confirm to continue, or go back and use ldaps:// or StartTLS.`}
                  confirmLabel={
                    confirmDetailString(transportWarning, "transport_kind") ===
                    "cleartext"
                      ? "Save without encryption"
                      : "Save without verification"
                  }
                  onConfirm={() => {
                    save(true);
                  }}
                  onCancel={() => {
                    setTransportWarning(null);
                  }}
                  pending={saving}
                />
              )}

            {saveError != null && (
              <p className="text-sm text-destructive">{saveError}</p>
            )}

            {/* Actions */}
            <div className="flex flex-wrap justify-end gap-2">
              <Button variant="outline" onClick={cancel}>
                Cancel
              </Button>
              <Button onClick={handleSave} disabled={saving}>
                {saving && <Loader2 className="h-4 w-4 animate-spin" />}
                {isNew ? "Create" : "Save"}
              </Button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
