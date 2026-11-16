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
  KeyRound,
  Plus,
  Trash2,
  Plug,
  Loader2,
  CheckCircle2,
  XCircle,
} from "lucide-react";
import { AdminNav } from "../components/AdminNav";
import { ConfirmRequiredWarning } from "@/components/ConfirmRequiredWarning";
import { PrivateAddressWarning } from "@/components/PrivateAddressWarning";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import {
  confirmDetailString,
  confirmRequiredFromError,
  type ConfirmRequired,
} from "@/lib/confirm-gate";
import { privateAddressWarningFromError } from "@/lib/private-address";

/** The backend's code for a cleartext OIDC callback (oidc.go). */
const INSECURE_OIDC_REDIRECT = "insecure_oidc_redirect_confirm_required";
/** The backend's code for an issuer on a private address (checkOIDCIssuer). */
const PRIVATE_ADDRESS = "private_address_confirm_required";

/** The confirmations a save carries, each given at its own prompt. */
interface SaveAcks {
  insecureRedirect: boolean;
  privateAddress: boolean;
}
const NO_ACKS: SaveAcks = { insecureRedirect: false, privateAddress: false };

/**
 * A setting whose control runs the full width under its label: an issuer or
 * a callback URL does not fit the narrow control column.
 */
const WIDE = "sm:grid-cols-1";
import {
  useOIDCConfigs,
  useCreateOIDCConfig,
  useUpdateOIDCConfig,
  useDeleteOIDCConfig,
  useTestOIDCConnection,
} from "../api/oidc-queries";
import { useRoles } from "../api/rbac-queries";
import type { OIDCConfig, OIDCConfigRequest } from "@/types/api";

type ProviderPreset =
  | "keycloak"
  | "authentik"
  | "azure"
  | "google"
  | "okta"
  | "custom";

const presets: Record<
  ProviderPreset,
  { label: string; hint: string; defaults: Partial<OIDCConfigRequest> }
> = {
  keycloak: {
    label: "Keycloak",
    hint: "Issuer: https://<host>/realms/<realm>",
    defaults: {
      scopes: ["openid", "email", "profile"],
      email_claim: "email",
      display_name_claim: "name",
      groups_claim: "groups",
    },
  },
  authentik: {
    label: "Authentik",
    hint: "Issuer: https://<host>/application/o/<slug>/",
    defaults: {
      scopes: ["openid", "email", "profile"],
      email_claim: "email",
      display_name_claim: "name",
      groups_claim: "groups",
    },
  },
  azure: {
    label: "Azure AD",
    hint: "Issuer: https://login.microsoftonline.com/<tenant-id>/v2.0",
    defaults: {
      scopes: ["openid", "email", "profile"],
      email_claim: "email",
      display_name_claim: "name",
      groups_claim: "groups",
    },
  },
  google: {
    label: "Google",
    hint: "Issuer: https://accounts.google.com",
    defaults: {
      issuer_url: "https://accounts.google.com",
      scopes: ["openid", "email", "profile"],
      email_claim: "email",
      display_name_claim: "name",
      groups_claim: "",
    },
  },
  okta: {
    label: "Okta",
    hint: "Issuer: https://<domain>.okta.com or https://<domain>.okta.com/oauth2/<auth-server-id>",
    defaults: {
      scopes: ["openid", "email", "profile", "groups"],
      email_claim: "email",
      display_name_claim: "name",
      groups_claim: "groups",
    },
  },
  custom: {
    label: "Custom",
    hint: "Enter your OIDC provider details manually",
    defaults: {},
  },
};

const emptyForm: OIDCConfigRequest = {
  name: "Default",
  enabled: false,
  issuer_url: "",
  client_id: "",
  client_secret: "",
  redirect_uri: "",
  scopes: ["openid", "email", "profile"],
  email_claim: "email",
  display_name_claim: "name",
  groups_claim: "groups",
  group_role_mapping: {},
  default_role_id: null,
  auto_provision: true,
  allowed_domains: [],
};

function configToForm(cfg: OIDCConfig): OIDCConfigRequest {
  return {
    name: cfg.name,
    enabled: cfg.enabled,
    issuer_url: cfg.issuer_url,
    client_id: cfg.client_id,
    client_secret: "",
    redirect_uri: cfg.redirect_uri,
    scopes: cfg.scopes,
    email_claim: cfg.email_claim,
    display_name_claim: cfg.display_name_claim,
    groups_claim: cfg.groups_claim,
    group_role_mapping: cfg.group_role_mapping,
    default_role_id: cfg.default_role_id,
    auto_provision: cfg.auto_provision,
    allowed_domains: cfg.allowed_domains,
  };
}

export function OIDCPage() {
  const { data: configs, isLoading } = useOIDCConfigs();
  const { data: roles } = useRoles();
  const createConfig = useCreateOIDCConfig();
  const updateConfig = useUpdateOIDCConfig();

  // Confirm gates the backend answers a save with rather than saving: a
  // plain-http callback (the authorization code rides back on that URL, so off
  // loopback it crosses the network in the clear) and an issuer on a private
  // address. One shows at a time; acks are what the refused save carried, so
  // confirming the second keeps the first.
  const [redirectWarning, setRedirectWarning] =
    useState<ConfirmRequired | null>(null);
  const [acks, setAcks] = useState<SaveAcks>(NO_ACKS);
  // This page had no error display for create/update at all, so a refusal was
  // simply a Save button that did nothing.
  const [saveError, setSaveError] = useState<string | null>(null);
  const deleteConfig = useDeleteOIDCConfig();
  const testConnection = useTestOIDCConnection();

  const [editingId, setEditingId] = useState<string | null>(null);
  const [form, setForm] = useState<OIDCConfigRequest>(emptyForm);
  const [isNew, setIsNew] = useState(false);
  const [providerPreset, setProviderPreset] =
    useState<ProviderPreset>("custom");
  const [testResult, setTestResult] = useState<{
    success: boolean;
    message: string;
  } | null>(null);

  // Group mapping rows
  const [mappingRows, setMappingRows] = useState<
    Array<{ group: string; roleId: string }>
  >([]);

  // Allowed domains as comma-separated text
  const [domainsText, setDomainsText] = useState("");

  // Scopes as comma-separated text
  const [scopesText, setScopesText] = useState("openid, email, profile");

  const activeConfig = configs?.find((c) => c.id === editingId);

  // The stored client secret is only ever sent to the issuer it was saved for
  // — the issuer names the token endpoint the secret is posted to — so moving
  // the issuer URL means re-entering it. The backend refuses the combination
  // outright (OIDCHandler.Update). A public client has no stored secret and is
  // not affected.
  const addressChanged =
    !isNew &&
    activeConfig != null &&
    activeConfig.client_secret_set &&
    form.issuer_url !== activeConfig.issuer_url;

  useEffect(() => {
    // Skip while a confirm is pending: re-seeding would replace the values the
    // operator is being asked about, so the confirm button would then submit
    // the config unchanged — silently discarding their edit while reporting
    // success.
    if (redirectWarning != null) return;
    if (activeConfig) {
      const f = configToForm(activeConfig);
      setForm(f);
      setMappingRows(
        Object.entries(activeConfig.group_role_mapping).map(
          ([group, roleId]) => ({ group, roleId }),
        ),
      );
      setDomainsText(activeConfig.allowed_domains.join(", "));
      setScopesText(activeConfig.scopes.join(", "));
    }
    // redirectWarning is read, not depended on, and deliberately absent from the
    // deps: including it would re-run this the moment the confirm clears,
    // re-seeding the form from the server and discarding the edit the operator
    // had just been asked to confirm.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeConfig]);

  // Clears the per-attempt feedback. The confirm in particular MUST be cleared
  // when the form switches configs: left mounted, its button would submit
  // whichever config is open now with an acknowledgement given for another.
  const clearFeedback = () => {
    setTestResult(null);
    setRedirectWarning(null);
    setSaveError(null);
  };

  const startNew = () => {
    const redirectUri = window.location.origin + "/api/v1/auth/oidc/callback";
    setForm({ ...emptyForm, redirect_uri: redirectUri });
    setMappingRows([]);
    setDomainsText("");
    setScopesText("openid, email, profile");
    setIsNew(true);
    setEditingId(null);
    clearFeedback();
    setProviderPreset("custom");
  };

  const startEdit = (cfg: OIDCConfig) => {
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
      if (row.group && row.roleId) {
        mapping[row.group] = row.roleId;
      }
    }
    return mapping;
  };

  const parseDomains = (): string[] => {
    if (!domainsText.trim()) return [];
    return domainsText
      .split(",")
      .map((d) => d.trim())
      .filter(Boolean);
  };

  const parseScopes = (): string[] => {
    if (!scopesText.trim()) return ["openid", "email", "profile"];
    return scopesText
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
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

  const save = (given: SaveAcks) => {
    const data: OIDCConfigRequest = {
      ...form,
      group_role_mapping: buildMappingFromRows(),
      allowed_domains: parseDomains(),
      scopes: parseScopes(),
      ...(given.insecureRedirect
        ? { acknowledge_insecure_redirect: true }
        : {}),
      ...(given.privateAddress ? { allow_private_address: true } : {}),
    };
    const target = saveTarget;
    // What a failure nobody is looking at names: the config as the operator
    // knows it, which is the stored name when an edit renames it.
    const stored = isNew ? undefined : activeConfig?.name;
    const named = `"${stored ?? data.name}"`;

    setSaveError(null);
    setAcks(given);
    if (!given.insecureRedirect && !given.privateAddress) {
      setRedirectWarning(null);
    }

    const onError = (err: unknown) => {
      const confirm = confirmRequiredFromError(
        err,
        [INSECURE_OIDC_REDIRECT, PRIVATE_ADDRESS],
        target,
      );
      if (confirm != null) {
        setRedirectWarning(confirm);
        return;
      }
      setRedirectWarning(null);
      setSaveError(
        err instanceof Error
          ? err.message
          : "Failed to save OIDC configuration",
      );
    };

    // Not once the form has been left: either success would close, and clear
    // the prompt of, whichever form has been opened in its place.
    if (isNew) {
      settle(createConfig.mutateAsync(data), {
        action: `Creating the OIDC configuration ${named}`,
        onSuccess: () => {
          setIsNew(false);
          setRedirectWarning(null);
        },
        onError,
      });
    } else if (editingId) {
      settle(updateConfig.mutateAsync({ ...data, id: editingId }), {
        action: `Saving the OIDC configuration ${named}`,
        onSuccess: () => {
          setEditingId(null);
          setRedirectWarning(null);
        },
        onError,
      });
    }
  };

  const handleSave = () => {
    save(NO_ACKS);
  };

  const handleTest = () => {
    if (!editingId) return;
    setTestResult(null);
    testConnection.mutate(editingId, {
      onSuccess: (result) => {
        setTestResult(result);
      },
      onError: (err) => {
        // An issuer that now resolves to a private address the configuration
        // has not confirmed: the server says how to confirm it.
        setTestResult({
          success: false,
          message:
            privateAddressWarningFromError(err) != null
              ? err.message
              : "Request failed",
        });
      },
    });
  };

  const showForm = isNew || editingId;

  if (isLoading) {
    return (
      <div>
        <AdminNav />
        <div className="flex h-64 items-center justify-center text-muted-foreground">
          Loading OIDC configuration...
        </div>
      </div>
    );
  }

  const total = configs?.length ?? 0;
  const enabledCount = configs?.filter((c) => c.enabled).length ?? 0;
  const saving = createConfig.isPending || updateConfig.isPending;
  const applyPreset = (key: ProviderPreset) => {
    setProviderPreset(key);
    if (key !== "custom") {
      const { defaults } = presets[key];
      setForm((prev) => ({ ...prev, ...defaults }));
      if (defaults.scopes) {
        setScopesText(defaults.scopes.join(", "));
      }
    }
  };

  return (
    <div>
      <AdminNav />
      <div className="space-y-5">
        <PageHeader
          title="OIDC / SSO"
          summary={
            total === 0
              ? "No identity provider is configured, so there is no single sign-on."
              : `${String(total)} identity provider${total !== 1 ? "s" : ""} configured, ${String(enabledCount)} enabled.`
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
                  <TableHead>Issuer</TableHead>
                  <TableHead>Status</TableHead>
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
                      {cfg.issuer_url}
                    </TableCell>
                    <TableCell>
                      {cfg.enabled ? (
                        <Badge variant="healthy">Enabled</Badge>
                      ) : (
                        <span className="text-muted-foreground">Disabled</span>
                      )}
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
                                "Delete this OIDC configuration? SSO users will no longer be able to log in.",
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
            icon={KeyRound}
            title="No OIDC configuration"
            description="Add a configuration to let people sign in through your identity provider."
          />
        )}

        {/* Edit/Create form */}
        {showForm && (
          <div className="max-w-3xl space-y-4">
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
              <h2 className="text-lg font-semibold">
                {isNew ? "New OIDC configuration" : "Edit OIDC configuration"}
              </h2>
              {!isNew && activeConfig != null && (
                <span className="text-sm text-muted-foreground">
                  {activeConfig.name}
                </span>
              )}
            </div>

            <SettingsSection title="Provider">
              <SettingRow
                className={WIDE}
                label="Provider preset"
                description={presets[providerPreset].hint}
              >
                {/* Full width, so the presets start at the label's edge. */}
                <div className="w-full">
                  <Segmented
                    aria-label="Provider preset"
                    size="md"
                    value={providerPreset}
                    onValueChange={applyPreset}
                    // Pressing the current preset again re-applies its defaults.
                    onReselect={applyPreset}
                    options={(
                      Object.entries(presets) as Array<
                        [ProviderPreset, (typeof presets)[ProviderPreset]]
                      >
                    ).map(([key, preset]) => ({
                      value: key,
                      label: preset.label,
                    }))}
                  />
                </div>
              </SettingRow>
              <SettingRow
                label="Name"
                description="What this configuration is called in the list."
                htmlFor="oidc-name"
              >
                <Input
                  id="oidc-name"
                  value={form.name}
                  onChange={(e) => {
                    setForm({ ...form, name: e.target.value });
                  }}
                  placeholder="Default"
                />
              </SettingRow>
              <SettingRow
                label="Enabled"
                description="People can sign in through this provider only while it is on."
                htmlFor="oidc-enabled"
              >
                <Switch
                  id="oidc-enabled"
                  checked={form.enabled}
                  onCheckedChange={(enabled) => {
                    setForm({ ...form, enabled });
                  }}
                />
              </SettingRow>
            </SettingsSection>

            <SettingsSection title="Client">
              <SettingRow
                className={WIDE}
                label="Issuer URL"
                description={
                  addressChanged
                    ? "This provider is moving to a new issuer, so it needs the client secret again. The stored one is only ever sent to the address it was saved for."
                    : !isNew &&
                        activeConfig?.allow_private_address === true &&
                        form.issuer_url === activeConfig.issuer_url
                      ? "Confirmed for a private network address. A new issuer is asked about again."
                      : undefined
                }
                htmlFor="oidc-issuer-url"
              >
                <Input
                  id="oidc-issuer-url"
                  className="font-mono"
                  value={form.issuer_url}
                  onChange={(e) => {
                    setForm({ ...form, issuer_url: e.target.value });
                  }}
                  placeholder="https://auth.example.com/realms/main"
                />
              </SettingRow>
              <SettingRow label="Client ID" htmlFor="oidc-client-id">
                <Input
                  id="oidc-client-id"
                  className="font-mono"
                  value={form.client_id}
                  onChange={(e) => {
                    setForm({ ...form, client_id: e.target.value });
                  }}
                  placeholder="nexara"
                />
              </SettingRow>
              <SettingRow
                label="Client secret"
                description={
                  !isNew && activeConfig?.client_secret_set
                    ? addressChanged
                      ? "Required: the issuer changed."
                      : "Leave blank to keep the current one."
                    : undefined
                }
                htmlFor="oidc-client-secret"
              >
                <Input
                  id="oidc-client-secret"
                  type="password"
                  value={form.client_secret}
                  onChange={(e) => {
                    setForm({ ...form, client_secret: e.target.value });
                  }}
                  placeholder={
                    addressChanged
                      ? "Re-enter the client secret for the new issuer"
                      : !isNew && activeConfig?.client_secret_set
                        ? "********"
                        : "Client secret"
                  }
                  required={addressChanged}
                />
              </SettingRow>
              <SettingRow
                className={WIDE}
                label="Redirect URI"
                description="Register this URL in your IdP as the redirect/callback URI"
                htmlFor="oidc-redirect-uri"
              >
                <Input
                  id="oidc-redirect-uri"
                  className="font-mono"
                  value={form.redirect_uri}
                  onChange={(e) => {
                    setForm({ ...form, redirect_uri: e.target.value });
                  }}
                  placeholder={
                    window.location.origin + "/api/v1/auth/oidc/callback"
                  }
                />
              </SettingRow>
            </SettingsSection>

            <SettingsSection
              title="Claims"
              description="Which claims in the ID token become the account's email, name and groups."
            >
              <SettingRow label="Email claim" htmlFor="oidc-email-claim">
                <Input
                  id="oidc-email-claim"
                  className="font-mono"
                  value={form.email_claim}
                  onChange={(e) => {
                    setForm({ ...form, email_claim: e.target.value });
                  }}
                  placeholder="email"
                />
              </SettingRow>
              <SettingRow
                label="Display name claim"
                htmlFor="oidc-display-name-claim"
              >
                <Input
                  id="oidc-display-name-claim"
                  className="font-mono"
                  value={form.display_name_claim}
                  onChange={(e) => {
                    setForm({ ...form, display_name_claim: e.target.value });
                  }}
                  placeholder="name"
                />
              </SettingRow>
              <SettingRow label="Groups claim" htmlFor="oidc-groups-claim">
                <Input
                  id="oidc-groups-claim"
                  className="font-mono"
                  value={form.groups_claim}
                  onChange={(e) => {
                    setForm({ ...form, groups_claim: e.target.value });
                  }}
                  placeholder="groups"
                />
              </SettingRow>
              <SettingRow
                label="Scopes"
                description="Comma-separated."
                htmlFor="oidc-scopes"
              >
                <Input
                  id="oidc-scopes"
                  className="font-mono"
                  value={scopesText}
                  onChange={(e) => {
                    setScopesText(e.target.value);
                  }}
                  placeholder="openid, email, profile"
                />
              </SettingRow>
            </SettingsSection>

            <SettingsSection title="Accounts">
              <SettingRow
                label="Auto-provision users"
                description="Automatically create accounts on first SSO login"
                htmlFor="oidc-auto-provision"
              >
                <Switch
                  id="oidc-auto-provision"
                  checked={form.auto_provision}
                  onCheckedChange={(auto_provision) => {
                    setForm({ ...form, auto_provision });
                  }}
                />
              </SettingRow>
              <SettingRow
                label="Allowed email domains"
                description="Comma-separated. Empty allows every domain."
                htmlFor="oidc-allowed-domains"
              >
                <Input
                  id="oidc-allowed-domains"
                  value={domainsText}
                  onChange={(e) => {
                    setDomainsText(e.target.value);
                  }}
                  placeholder="example.com, corp.example.com"
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
                    setMappingRows([...mappingRows, { group: "", roleId: "" }]);
                  }}
                >
                  <Plus className="h-3.5 w-3.5" />
                  Add mapping
                </Button>
              }
            >
              {mappingRows.length === 0 ? (
                <p className="pb-3.5 pt-1 text-sm text-muted-foreground">
                  No mappings configured. SSO users will be assigned the default
                  role.
                </p>
              ) : (
                <div className="space-y-2 pb-3.5 pt-1">
                  {mappingRows.map((row, idx) => (
                    <div
                      key={idx}
                      className="flex flex-wrap items-center gap-2"
                    >
                      <Input
                        aria-label={`Group of mapping ${String(idx + 1)}`}
                        className="min-w-0 flex-[2_1_14rem]"
                        value={row.group}
                        onChange={(e) => {
                          const updated = [...mappingRows];
                          updated[idx] = { ...row, group: e.target.value };
                          setMappingRows(updated);
                        }}
                        placeholder="IdP group name"
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
                        aria-label={`Remove group mapping ${row.group || String(idx + 1)}`}
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
                htmlFor="oidc-default-role"
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
                    id="oidc-default-role"
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

            {/* Test connection: the saved configuration, not the edits above */}
            {!isNew && editingId && (
              <SettingsSection
                title="Test connection"
                description="Tries the configuration as it is saved. Edits above are not tested until they are saved."
              >
                <SettingRow
                  label="Discovery endpoint"
                  description="Asks the issuer for its OpenID configuration."
                >
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
                    Test discovery endpoint
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

            {redirectWarning != null &&
              redirectWarning.target === saveTarget &&
              redirectWarning.code === PRIVATE_ADDRESS && (
                <PrivateAddressWarning
                  ip={confirmDetailString(redirectWarning, "ip") ?? ""}
                  url={form.issuer_url}
                  privateOnly
                  onConfirm={() => {
                    save({ ...acks, privateAddress: true });
                  }}
                  onCancel={() => {
                    setRedirectWarning(null);
                  }}
                  pending={saving}
                />
              )}

            {redirectWarning != null &&
              redirectWarning.target === saveTarget &&
              redirectWarning.code === INSECURE_OIDC_REDIRECT && (
                <ConfirmRequiredWarning
                  title="Callback is not encrypted"
                  message={`${redirectWarning.message} This is fine for a self-hosted lab with no TLS — confirm to continue, or go back and use an https callback.`}
                  confirmLabel="Save with a cleartext callback"
                  onConfirm={() => {
                    save({ ...acks, insecureRedirect: true });
                  }}
                  onCancel={() => {
                    setRedirectWarning(null);
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
