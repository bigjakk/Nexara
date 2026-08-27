import { useState, useEffect } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Plus,
  Trash2,
  Plug,
  Loader2,
  CheckCircle2,
  XCircle,
} from "lucide-react";
import { AdminNav } from "../components/AdminNav";
import { ConfirmRequiredWarning } from "@/components/ConfirmRequiredWarning";
import {
  confirmRequiredFromError,
  type ConfirmRequired,
} from "@/lib/confirm-gate";

/** The backend's code for a cleartext OIDC callback (oidc.go). */
const INSECURE_OIDC_REDIRECT = "insecure_oidc_redirect_confirm_required";
import {
  useOIDCConfigs,
  useCreateOIDCConfig,
  useUpdateOIDCConfig,
  useDeleteOIDCConfig,
  useTestOIDCConnection,
} from "../api/oidc-queries";
import { useRoles } from "../api/rbac-queries";
import type { OIDCConfig, OIDCConfigRequest } from "@/types/api";

type ProviderPreset = "keycloak" | "authentik" | "azure" | "google" | "okta" | "custom";

const presets: Record<ProviderPreset, { label: string; hint: string; defaults: Partial<OIDCConfigRequest> }> = {
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

  // Confirm gate for a plain-http callback: the authorization code rides back
  // on that URL, so off loopback it crosses the network in the clear. The
  // backend refuses the first attempt rather than saving.
  const [redirectWarning, setRedirectWarning] = useState<ConfirmRequired | null>(
    null,
  );
  // This page had no error display for create/update at all, so a refusal was
  // simply a Save button that did nothing.
  const [saveError, setSaveError] = useState<string | null>(null);
  const deleteConfig = useDeleteOIDCConfig();
  const testConnection = useTestOIDCConnection();

  const [editingId, setEditingId] = useState<string | null>(null);
  const [form, setForm] = useState<OIDCConfigRequest>(emptyForm);
  const [isNew, setIsNew] = useState(false);
  const [providerPreset, setProviderPreset] = useState<ProviderPreset>("custom");
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

  const save = (acknowledgeInsecureRedirect: boolean) => {
    const data: OIDCConfigRequest = {
      ...form,
      group_role_mapping: buildMappingFromRows(),
      allowed_domains: parseDomains(),
      scopes: parseScopes(),
      ...(acknowledgeInsecureRedirect
        ? { acknowledge_insecure_redirect: true }
        : {}),
    };
    const target = saveTarget;

    setSaveError(null);
    if (!acknowledgeInsecureRedirect) {
      setRedirectWarning(null);
    }

    const onError = (err: unknown) => {
      const confirm = confirmRequiredFromError(err, [INSECURE_OIDC_REDIRECT], target);
      if (confirm != null) {
        setRedirectWarning(confirm);
        return;
      }
      setRedirectWarning(null);
      setSaveError(
        err instanceof Error ? err.message : "Failed to save OIDC configuration",
      );
    };

    if (isNew) {
      createConfig.mutate(data, {
        onSuccess: () => {
          setIsNew(false);
          setRedirectWarning(null);
        },
        onError,
      });
    } else if (editingId) {
      updateConfig.mutate(
        { ...data, id: editingId },
        {
          onSuccess: () => {
            setEditingId(null);
            setRedirectWarning(null);
          },
          onError,
        },
      );
    }
  };

  const handleSave = () => {
    save(false);
  };

  const handleTest = () => {
    if (!editingId) return;
    setTestResult(null);
    testConnection.mutate(editingId, {
      onSuccess: (result) => {
        setTestResult(result);
      },
      onError: () => {
        setTestResult({ success: false, message: "Request failed" });
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

  return (
    <div>
      <AdminNav />
      <div className="space-y-6 p-6">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold tracking-tight">OIDC / SSO</h1>
            <p className="text-muted-foreground">
              Configure OpenID Connect single sign-on with your identity provider
            </p>
          </div>
          {!showForm && (
            <Button onClick={startNew}>
              <Plus className="mr-2 h-4 w-4" />
              Add Config
            </Button>
          )}
        </div>

        {/* Config list */}
        {!showForm && configs && configs.length > 0 && (
          <div className="space-y-3">
            {configs.map((cfg) => (
              <div
                key={cfg.id}
                className="flex items-center justify-between rounded-lg border p-4"
              >
                <div className="flex items-center gap-3">
                  <Badge variant={cfg.enabled ? "default" : "secondary"}>
                    {cfg.enabled ? "Enabled" : "Disabled"}
                  </Badge>
                  <div>
                    <p className="font-medium">{cfg.name}</p>
                    <p className="text-sm text-muted-foreground">
                      {cfg.issuer_url}
                    </p>
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => { startEdit(cfg); }}
                  >
                    Edit
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="text-destructive"
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
              </div>
            ))}
          </div>
        )}

        {!showForm && (!configs || configs.length === 0) && (
          <div className="rounded-lg border border-dashed p-8 text-center text-muted-foreground">
            No OIDC configurations. Click "Add Config" to set up SSO.
          </div>
        )}

        {/* Edit/Create form */}
        {showForm && (
          <div className="space-y-6 rounded-lg border p-6">
            <h2 className="text-lg font-semibold">
              {isNew ? "New OIDC Configuration" : "Edit OIDC Configuration"}
            </h2>

            {/* Provider preset */}
            <div>
              <Label className="mb-2 block">Provider Preset</Label>
              <div className="flex flex-wrap gap-2">
                {(Object.entries(presets) as Array<[ProviderPreset, typeof presets[ProviderPreset]]>).map(([key, preset]) => (
                  <Button
                    key={key}
                    variant={providerPreset === key ? "default" : "outline"}
                    size="sm"
                    onClick={() => {
                      setProviderPreset(key);
                      if (key !== "custom") {
                        setForm((prev) => ({ ...prev, ...preset.defaults }));
                        if (preset.defaults.scopes) {
                          setScopesText(preset.defaults.scopes.join(", "));
                        }
                      }
                    }}
                  >
                    {preset.label}
                  </Button>
                ))}
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                {presets[providerPreset].hint}
              </p>
            </div>

            {/* Basic settings */}
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label>Name</Label>
                <Input
                  value={form.name}
                  onChange={(e) => { setForm({ ...form, name: e.target.value }); }}
                  placeholder="Default"
                />
              </div>
              <div className="flex items-center gap-3 pt-6">
                <Switch
                  checked={form.enabled}
                  onCheckedChange={(enabled) => { setForm({ ...form, enabled }); }}
                />
                <Label>Enabled</Label>
              </div>
            </div>

            {/* Provider settings */}
            <div>
              <h3 className="mb-3 font-medium">Provider</h3>
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>Issuer URL</Label>
                  <Input
                    value={form.issuer_url}
                    onChange={(e) => { setForm({ ...form, issuer_url: e.target.value }); }}
                    placeholder="https://auth.example.com/realms/main"
                  />
                  {addressChanged && (
                    <p className="text-xs text-muted-foreground">
                      This provider is moving to a new issuer, so it needs the
                      client secret again — the stored one is only ever sent to
                      the address it was saved for.
                    </p>
                  )}
                </div>
                <div className="space-y-2">
                  <Label>Client ID</Label>
                  <Input
                    value={form.client_id}
                    onChange={(e) => { setForm({ ...form, client_id: e.target.value }); }}
                    placeholder="nexara"
                  />
                </div>
                <div className="space-y-2">
                  <Label>
                    Client Secret
                    {!isNew && activeConfig?.client_secret_set && (
                      <span className="ml-2 text-xs text-muted-foreground">
                        {addressChanged
                          ? "(required — the issuer changed)"
                          : "(leave blank to keep current)"}
                      </span>
                    )}
                  </Label>
                  <Input
                    type="password"
                    value={form.client_secret}
                    onChange={(e) => { setForm({ ...form, client_secret: e.target.value }); }}
                    placeholder={
                      addressChanged
                        ? "Re-enter the client secret for the new issuer"
                        : !isNew && activeConfig?.client_secret_set
                          ? "********"
                          : "Client secret"
                    }
                    required={addressChanged}
                  />
                </div>
                <div className="space-y-2">
                  <Label>Redirect URI</Label>
                  <Input
                    value={form.redirect_uri}
                    onChange={(e) => { setForm({ ...form, redirect_uri: e.target.value }); }}
                    placeholder={window.location.origin + "/api/v1/auth/oidc/callback"}
                  />
                  <p className="text-xs text-muted-foreground">
                    Register this URL in your IdP as the redirect/callback URI
                  </p>
                </div>
              </div>
            </div>

            {/* Claims mapping */}
            <div>
              <h3 className="mb-3 font-medium">Claims Mapping</h3>
              <div className="grid gap-4 sm:grid-cols-3">
                <div className="space-y-2">
                  <Label>Email Claim</Label>
                  <Input
                    value={form.email_claim}
                    onChange={(e) => { setForm({ ...form, email_claim: e.target.value }); }}
                    placeholder="email"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Display Name Claim</Label>
                  <Input
                    value={form.display_name_claim}
                    onChange={(e) => { setForm({ ...form, display_name_claim: e.target.value }); }}
                    placeholder="name"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Groups Claim</Label>
                  <Input
                    value={form.groups_claim}
                    onChange={(e) => { setForm({ ...form, groups_claim: e.target.value }); }}
                    placeholder="groups"
                  />
                </div>
              </div>
            </div>

            {/* Scopes */}
            <div className="space-y-2">
              <Label>Scopes (comma-separated)</Label>
              <Input
                value={scopesText}
                onChange={(e) => { setScopesText(e.target.value); }}
                placeholder="openid, email, profile"
              />
            </div>

            {/* Auto-provision and allowed domains */}
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="flex items-center gap-3">
                <Switch
                  checked={form.auto_provision}
                  onCheckedChange={(auto_provision) => { setForm({ ...form, auto_provision }); }}
                />
                <div>
                  <Label>Auto-Provision Users</Label>
                  <p className="text-xs text-muted-foreground">
                    Automatically create accounts on first SSO login
                  </p>
                </div>
              </div>
              <div className="space-y-2">
                <Label>Allowed Email Domains (comma-separated, empty = all)</Label>
                <Input
                  value={domainsText}
                  onChange={(e) => { setDomainsText(e.target.value); }}
                  placeholder="example.com, corp.example.com"
                />
              </div>
            </div>

            {/* Group-to-Role Mapping */}
            <div>
              <div className="mb-3 flex items-center justify-between">
                <h3 className="font-medium">Group-to-Role Mapping</h3>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    setMappingRows([...mappingRows, { group: "", roleId: "" }]);
                  }}
                >
                  <Plus className="mr-1 h-3 w-3" />
                  Add Mapping
                </Button>
              </div>
              {mappingRows.length === 0 && (
                <p className="text-sm text-muted-foreground">
                  No mappings configured. SSO users will be assigned the default role.
                </p>
              )}
              <div className="space-y-2">
                {mappingRows.map((row, idx) => (
                  <div key={idx} className="flex items-center gap-2">
                    <Input
                      className="flex-1"
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
                      <SelectTrigger className="w-48">
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
                      variant="ghost"
                      size="icon"
                      className="text-destructive"
                      onClick={() => {
                        setMappingRows(mappingRows.filter((_, i) => i !== idx));
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                ))}
              </div>
            </div>

            {/* Default role */}
            <div className="max-w-sm space-y-2">
              <Label>Default Role (no group match)</Label>
              <Select
                value={form.default_role_id ?? "none"}
                onValueChange={(val) => {
                  setForm({
                    ...form,
                    default_role_id: val === "none" ? null : val,
                  });
                }}
              >
                <SelectTrigger>
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
            </div>

            {/* Test connection */}
            {!isNew && editingId && (
              <div className="rounded-lg border bg-muted/50 p-4">
                <h3 className="mb-3 font-medium">Test Connection</h3>
                <Button
                  variant="outline"
                  onClick={handleTest}
                  disabled={testConnection.isPending}
                >
                  {testConnection.isPending ? (
                    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                  ) : (
                    <Plug className="mr-2 h-4 w-4" />
                  )}
                  Test Discovery Endpoint
                </Button>
                {testResult && (
                  <div
                    className={`mt-2 flex items-center gap-2 text-sm ${testResult.success ? "text-emerald-600" : "text-destructive"}`}
                  >
                    {testResult.success ? (
                      <CheckCircle2 className="h-4 w-4" />
                    ) : (
                      <XCircle className="h-4 w-4" />
                    )}
                    {testResult.message}
                  </div>
                )}
              </div>
            )}

            {redirectWarning != null && redirectWarning.target === saveTarget && (
              <ConfirmRequiredWarning
                title="Callback is not encrypted"
                message={`${redirectWarning.message} This is fine for a self-hosted lab with no TLS — confirm to continue, or go back and use an https callback.`}
                confirmLabel="Save with a cleartext callback"
                onConfirm={() => {
                  save(true);
                }}
                onCancel={() => {
                  setRedirectWarning(null);
                }}
                pending={createConfig.isPending || updateConfig.isPending}
              />
            )}

            {saveError != null && (
              <p className="text-sm text-destructive">{saveError}</p>
            )}

            {/* Actions */}
            <div className="flex gap-2">
              <Button onClick={handleSave} disabled={createConfig.isPending || updateConfig.isPending}>
                {(createConfig.isPending || updateConfig.isPending) && (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                )}
                {isNew ? "Create" : "Save"}
              </Button>
              <Button variant="outline" onClick={cancel}>
                Cancel
              </Button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
