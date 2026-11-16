import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
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
  {
    labelKey: "openLDAP" | "activeDirectory";
    defaults: Partial<LDAPConfigRequest>;
  }
> = {
  openldap: {
    labelKey: "openLDAP",
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
    labelKey: "activeDirectory",
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
  const { t } = useTranslation("admin");
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
    const named = stored ?? data.name;

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
        err instanceof Error ? err.message : t("ldapPage.saveFailed"),
      );
    };

    // Not once the form has been left: either success would close, and clear
    // the prompt of, whichever form has been opened in its place.
    if (isNew) {
      settle(createConfig.mutateAsync(data), {
        action: t("ldapPage.creatingAction", { name: named }),
        onSuccess: () => {
          setIsNew(false);
          onSettledSuccess();
        },
        onError,
      });
    } else if (editingId) {
      settle(updateConfig.mutateAsync({ ...data, id: editingId }), {
        action: t("ldapPage.savingAction", { name: named }),
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
      action: t("ldapPage.testingAction", {
        name: activeConfig?.name ?? form.name,
      }),
      onSuccess: (result) => {
        setTestResult(result);
      },
      onError: (err) => {
        setTestResult({
          success: false,
          message: describeError(err) || t("authConfig.requestFailed"),
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
        action: t("ldapPage.syncingAction", {
          name: activeConfig?.name ?? form.name,
        }),
        onSuccess: (result) => {
          const parts = [
            t("ldapPage.syncedCount", { count: result.users_synced }),
          ];
          if (result.users_disabled > 0) {
            parts.push(
              t("ldapPage.disabledCount", { count: result.users_disabled }),
            );
          }
          if (result.users_re_enabled > 0) {
            parts.push(
              t("ldapPage.reEnabledCount", { count: result.users_re_enabled }),
            );
          }
          if ((result.users_kept_active ?? 0) > 0) {
            parts.push(
              t("ldapPage.keptActiveCount", {
                count: result.users_kept_active,
              }),
            );
          }
          setSyncResult(
            t("ldapPage.syncResult", {
              message: result.message,
              details: parts.join(t("ldapPage.syncResultSeparator")),
            }),
          );
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
          setSyncResult(
            reason
              ? t("ldapPage.syncFailedReason", { reason })
              : t("ldapPage.syncFailed"),
          );
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
          {t("loadingLDAP")}
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
          title={t("ldapActiveDirectory")}
          summary={
            total === 0
              ? t("ldapPage.summaryNone")
              : t("ldapPage.summary", { count: total, enabled: enabledCount })
          }
          actions={
            !showForm ? (
              <Button onClick={startNew}>
                <Plus className="h-4 w-4" />
                {t("authConfig.addConfiguration")}
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
                  <TableHead>{t("common:name")}</TableHead>
                  <TableHead>{t("ldapPage.server")}</TableHead>
                  <TableHead>{t("common:status")}</TableHead>
                  <TableHead className="whitespace-nowrap">
                    {t("ldapPage.lastSync")}
                  </TableHead>
                  <TableHead className="w-32">
                    <span className="sr-only">{t("common:actions")}</span>
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
                        <Badge variant="healthy">{t("common:enabled")}</Badge>
                      ) : (
                        <span className="text-muted-foreground">
                          {t("common:disabled")}
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      <DateTime
                        value={cfg.last_sync_at}
                        fallback={t("ldapPage.never")}
                      />
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
                          {t("common:edit")}
                        </Button>
                        <Button
                          aria-label={t("authConfig.deleteLabel", {
                            name: cfg.name,
                          })}
                          variant="ghost"
                          size="icon"
                          className="h-8 w-8 text-muted-foreground hover:text-destructive"
                          onClick={() => {
                            if (confirm(t("deleteLDAPConfirm"))) {
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
            title={t("ldapPage.emptyTitle")}
            description={t("ldapPage.emptyDescription")}
          />
        )}

        {/* Edit/Create form */}
        {showForm && (
          <div className="max-w-3xl space-y-4">
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
              <h2 className="text-lg font-semibold">
                {isNew ? t("ldapPage.newTitle") : t("ldapPage.editTitle")}
              </h2>
              {!isNew && activeConfig != null && (
                <span className="text-sm text-muted-foreground">
                  {activeConfig.name}
                </span>
              )}
            </div>

            <SettingsSection title={t("ldapPage.directory")}>
              <SettingRow
                label={t("ldapPage.directoryType")}
                description={
                  directoryType === "ad"
                    ? t("prefilledActiveDirectory")
                    : t("prefilledOpenLDAP")
                }
              >
                <Segmented
                  aria-label={t("ldapPage.directoryType")}
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
                    label: t(preset.labelKey),
                  }))}
                />
              </SettingRow>
              <SettingRow
                label={t("common:name")}
                description={t("authConfig.nameDescription")}
                htmlFor="ldap-name"
              >
                <Input
                  id="ldap-name"
                  value={form.name}
                  onChange={(e) => {
                    setForm({ ...form, name: e.target.value });
                  }}
                  placeholder={t("authConfig.namePlaceholder")}
                />
              </SettingRow>
              <SettingRow
                label={t("common:enabled")}
                description={t("ldapPage.enabledDescription")}
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

            <SettingsSection title={t("connection")}>
              <SettingRow
                className={WIDE}
                label={t("serverUrl")}
                description={
                  addressChanged
                    ? t("ldapPage.serverUrlMoved")
                    : t("ldapPage.serverUrlDescription")
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
                label={t("startTLS")}
                description={t("ldapPage.startTLSDescription")}
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
                label={t("ldapPage.skipTLSVerify")}
                description={t("ldapPage.skipTLSVerifyDescription")}
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
                label={t("bindDN")}
                description={t("ldapPage.bindDNDescription")}
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
                label={t("ldapPage.bindPassword")}
                description={
                  !isNew && activeConfig?.bind_password_set
                    ? addressChanged
                      ? t("ldapPage.bindPasswordRequired")
                      : t("authConfig.keepCurrent")
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
                      ? t("ldapPage.bindPasswordReenter")
                      : !isNew && activeConfig?.bind_password_set
                        ? "********"
                        : t("ldapPage.passwordPlaceholder")
                  }
                  required={addressChanged}
                />
              </SettingRow>
            </SettingsSection>

            <SettingsSection
              title={t("ldapPage.userSearch")}
              description={t("ldapPage.userSearchDescription")}
            >
              <SettingRow
                className={WIDE}
                label={t("ldapPage.searchBaseDN")}
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
                label={t("ldapPage.userFilter")}
                description={t("ldapPage.userFilterDescription", {
                  placeholder: "{{username}}",
                })}
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
                label={t("ldapPage.usernameAttribute")}
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
                label={t("ldapPage.emailAttribute")}
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
                label={t("ldapPage.displayNameAttribute")}
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

            <SettingsSection title={t("ldapPage.groupSearch")}>
              <SettingRow
                className={WIDE}
                label={t("ldapPage.groupSearchBaseDN")}
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
                label={t("ldapPage.groupFilter")}
                description={t("ldapPage.groupFilterDescription", {
                  placeholder: "{{userDN}}",
                })}
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
                label={t("ldapPage.groupAttribute")}
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
              title={t("authConfig.groupToRoleMapping")}
              description={t("authConfig.groupToRoleMappingDescription")}
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
                  {t("authConfig.addMapping")}
                </Button>
              }
            >
              {mappingRows.length === 0 ? (
                <p className="pb-3.5 pt-1 text-sm text-muted-foreground">
                  {t("ldapPage.noMappings")}
                </p>
              ) : (
                <div className="space-y-2 pb-3.5 pt-1">
                  {mappingRows.map((row, idx) => (
                    <div
                      key={idx}
                      className="flex flex-wrap items-center gap-2"
                    >
                      <Input
                        aria-label={t("ldapPage.mappingGroupDNLabel", {
                          index: idx + 1,
                        })}
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
                          aria-label={t("authConfig.mappingRoleLabel", {
                            index: idx + 1,
                          })}
                          className="min-w-0 flex-[1_1_10rem]"
                        >
                          <SelectValue placeholder={t("selectRole")} />
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
                        aria-label={t("authConfig.removeMappingLabel", {
                          mapping: row.groupDN || String(idx + 1),
                        })}
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
                label={t("authConfig.defaultRole")}
                description={t("authConfig.defaultRoleDescription")}
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
                    <SelectValue placeholder={t("noDefaultRole")} />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="none">{t("noDefaultRole")}</SelectItem>
                    {roles?.map((role) => (
                      <SelectItem key={role.id} value={role.id}>
                        {role.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </SettingRow>
            </SettingsSection>

            <SettingsSection title={t("ldapPage.sync")}>
              <SettingRow
                label={t("ldapPage.syncInterval")}
                description={t("ldapPage.syncIntervalDescription")}
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
                <span className="text-sm text-muted-foreground">
                  {t("ldapPage.minutes")}
                </span>
              </SettingRow>
              {!isNew && editingId && (
                <SettingRow
                  label={t("ldapPage.syncNow")}
                  description={syncResult ?? t("ldapPage.syncNowDescription")}
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
                    {t("ldapPage.syncNow")}
                  </Button>
                </SettingRow>
              )}
              {syncWarning != null && syncWarning.target === editingId && (
                <div className="pb-3.5">
                  <ConfirmRequiredWarning
                    title={t("ldapPage.deactivateTitle", {
                      count: missingCount(syncWarning),
                    })}
                    message={t("ldapPage.deactivateMessage", {
                      count: missingCount(syncWarning),
                    })}
                    confirmLabel={t("ldapPage.deactivateConfirm", {
                      count: missingCount(syncWarning),
                    })}
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
                title={t("authConfig.testConnection")}
                description={t("authConfig.testConnectionDescription")}
              >
                <SettingRow
                  label={t("ldapPage.testUsername")}
                  description={t("ldapPage.optional")}
                  htmlFor="ldap-test-username"
                >
                  <Input
                    id="ldap-test-username"
                    className="min-w-0 flex-1"
                    value={testUsername}
                    onChange={(e) => {
                      setTestUsername(e.target.value);
                    }}
                    placeholder={t("testUsernamePlaceholder")}
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
                    {t("common:test")}
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
                      ? t("ldapPage.cleartextTitle")
                      : t("ldapPage.unverifiedTitle")
                  }
                  message={t("ldapPage.transportMessage", {
                    message: transportWarning.message,
                  })}
                  confirmLabel={
                    confirmDetailString(transportWarning, "transport_kind") ===
                    "cleartext"
                      ? t("ldapPage.saveWithoutEncryption")
                      : t("ldapPage.saveWithoutVerification")
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
                {t("common:cancel")}
              </Button>
              <Button onClick={handleSave} disabled={saving}>
                {saving && <Loader2 className="h-4 w-4 animate-spin" />}
                {isNew ? t("common:create") : t("common:save")}
              </Button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
