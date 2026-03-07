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

type DirectoryType = "openldap" | "ad";

const presets: Record<DirectoryType, { label: string; defaults: Partial<LDAPConfigRequest> }> = {
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
      user_filter: "(|(sAMAccountName={{username}})(userPrincipalName={{username}})(mail={{username}}))",
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
  if (cfg.username_attribute === "sAMAccountName" || cfg.user_filter.includes("sAMAccountName")) {
    return "ad";
  }
  return "openldap";
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

  // Group mapping rows for editor
  const [mappingRows, setMappingRows] = useState<
    Array<{ groupDN: string; roleId: string }>
  >([]);

  const activeConfig = configs?.find((c) => c.id === editingId);

  useEffect(() => {
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
  }, [activeConfig]);

  const startNew = () => {
    setForm(emptyForm);
    setMappingRows([]);
    setIsNew(true);
    setEditingId(null);
    setTestResult(null);
    setSyncResult(null);
  };

  const startEdit = (cfg: LDAPConfig) => {
    setEditingId(cfg.id);
    setIsNew(false);
    setTestResult(null);
    setSyncResult(null);
  };

  const cancel = () => {
    setEditingId(null);
    setIsNew(false);
    setTestResult(null);
    setSyncResult(null);
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

  const handleSave = () => {
    const data: LDAPConfigRequest = {
      ...form,
      group_role_mapping: buildMappingFromRows(),
    };

    if (isNew) {
      createConfig.mutate(data, {
        onSuccess: () => {
          setIsNew(false);
        },
      });
    } else if (editingId) {
      updateConfig.mutate(
        { ...data, id: editingId },
        {
          onSuccess: () => {
            setEditingId(null);
          },
        },
      );
    }
  };

  const handleTest = () => {
    if (!editingId) return;
    setTestResult(null);
    const testPayload: { id: string; test_username?: string } = { id: editingId };
    if (testUsername) {
      testPayload.test_username = testUsername;
    }
    testConnection.mutate(
      testPayload,
      {
        onSuccess: (result) => {
          setTestResult(result);
        },
        onError: () => {
          setTestResult({ success: false, message: "Request failed" });
        },
      },
    );
  };

  const handleSync = () => {
    if (!editingId) return;
    setSyncResult(null);
    syncLDAP.mutate(editingId, {
      onSuccess: (result) => {
        const parts = [`${String(result.users_synced)} synced`];
        if (result.users_disabled > 0) {
          parts.push(`${String(result.users_disabled)} disabled`);
        }
        if (result.users_re_enabled > 0) {
          parts.push(`${String(result.users_re_enabled)} re-enabled`);
        }
        setSyncResult(`${result.message} (${parts.join(", ")})`);
      },
      onError: () => {
        setSyncResult("Sync failed");
      },
    });
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

  return (
    <div>
      <AdminNav />
      <div className="space-y-6 p-6">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold">LDAP / Active Directory</h1>
            <p className="text-muted-foreground">
              Configure LDAP/AD authentication and group-to-role mapping
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
                      {cfg.server_url}
                    </p>
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  {cfg.last_sync_at && (
                    <span className="text-xs text-muted-foreground">
                      Last sync: {new Date(cfg.last_sync_at).toLocaleString()}
                    </span>
                  )}
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
              </div>
            ))}
          </div>
        )}

        {!showForm && (!configs || configs.length === 0) && (
          <div className="rounded-lg border border-dashed p-8 text-center text-muted-foreground">
            No LDAP configurations. Click "Add Config" to set up LDAP/AD
            authentication.
          </div>
        )}

        {/* Edit/Create form */}
        {showForm && (
          <div className="space-y-6 rounded-lg border p-6">
            <h2 className="text-lg font-semibold">
              {isNew ? "New LDAP Configuration" : "Edit LDAP Configuration"}
            </h2>

            {/* Directory type preset */}
            <div>
              <Label className="mb-2 block">Directory Type</Label>
              <div className="flex gap-2">
                {(Object.entries(presets) as Array<[DirectoryType, typeof presets[DirectoryType]]>).map(([key, preset]) => (
                  <Button
                    key={key}
                    variant={directoryType === key ? "default" : "outline"}
                    size="sm"
                    onClick={() => {
                      setDirectoryType(key);
                      setForm((prev) => ({ ...prev, ...preset.defaults }));
                    }}
                  >
                    {preset.label}
                  </Button>
                ))}
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                {directoryType === "ad"
                  ? "Pre-filled for Active Directory (sAMAccountName, userPrincipalName, displayName)"
                  : "Pre-filled for OpenLDAP (uid, mail, cn)"}
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

            {/* Connection settings */}
            <div>
              <h3 className="mb-3 font-medium">Connection</h3>
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>Server URL</Label>
                  <Input
                    value={form.server_url}
                    onChange={(e) => { setForm({ ...form, server_url: e.target.value }); }}
                    placeholder="ldap://ldap.example.com:389"
                  />
                </div>
                <div className="flex items-center gap-6 pt-6">
                  <div className="flex items-center gap-2">
                    <Switch
                      checked={form.start_tls}
                      onCheckedChange={(start_tls) => { setForm({ ...form, start_tls }); }}
                    />
                    <Label>StartTLS</Label>
                  </div>
                  <div className="flex items-center gap-2">
                    <Switch
                      checked={form.skip_tls_verify}
                      onCheckedChange={(skip_tls_verify) => { setForm({ ...form, skip_tls_verify }); }}
                    />
                    <Label>Skip TLS Verify</Label>
                  </div>
                </div>
              </div>
            </div>

            {/* Bind settings */}
            <div>
              <h3 className="mb-3 font-medium">Bind Credentials</h3>
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>Bind DN</Label>
                  <Input
                    value={form.bind_dn}
                    onChange={(e) => { setForm({ ...form, bind_dn: e.target.value }); }}
                    placeholder="cn=admin,dc=example,dc=com"
                  />
                </div>
                <div className="space-y-2">
                  <Label>
                    Bind Password
                    {!isNew && activeConfig?.bind_password_set && (
                      <span className="ml-2 text-xs text-muted-foreground">
                        (leave blank to keep current)
                      </span>
                    )}
                  </Label>
                  <Input
                    type="password"
                    value={form.bind_password}
                    onChange={(e) => { setForm({ ...form, bind_password: e.target.value }); }}
                    placeholder={
                      !isNew && activeConfig?.bind_password_set
                        ? "********"
                        : "Password"
                    }
                  />
                </div>
              </div>
            </div>

            {/* Search settings */}
            <div>
              <h3 className="mb-3 font-medium">User Search</h3>
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>Search Base DN</Label>
                  <Input
                    value={form.search_base_dn}
                    onChange={(e) => { setForm({ ...form, search_base_dn: e.target.value }); }}
                    placeholder="dc=example,dc=com"
                  />
                </div>
                <div className="space-y-2">
                  <Label>User Filter</Label>
                  <Input
                    value={form.user_filter}
                    onChange={(e) => { setForm({ ...form, user_filter: e.target.value }); }}
                    placeholder="(|(uid={{username}})(mail={{username}}))"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Username Attribute</Label>
                  <Input
                    value={form.username_attribute}
                    onChange={(e) => { setForm({ ...form, username_attribute: e.target.value }); }}
                    placeholder="uid"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Email Attribute</Label>
                  <Input
                    value={form.email_attribute}
                    onChange={(e) => { setForm({ ...form, email_attribute: e.target.value }); }}
                    placeholder="mail"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Display Name Attribute</Label>
                  <Input
                    value={form.display_name_attribute}
                    onChange={(e) => { setForm({ ...form, display_name_attribute: e.target.value }); }}
                    placeholder="cn"
                  />
                </div>
              </div>
            </div>

            {/* Group settings */}
            <div>
              <h3 className="mb-3 font-medium">Group Search</h3>
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label>Group Search Base DN</Label>
                  <Input
                    value={form.group_search_base_dn}
                    onChange={(e) => { setForm({ ...form, group_search_base_dn: e.target.value }); }}
                    placeholder="ou=groups,dc=example,dc=com"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Group Filter</Label>
                  <Input
                    value={form.group_filter}
                    onChange={(e) => { setForm({ ...form, group_filter: e.target.value }); }}
                    placeholder="(member={{userDN}})"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Group Attribute</Label>
                  <Input
                    value={form.group_attribute}
                    onChange={(e) => { setForm({ ...form, group_attribute: e.target.value }); }}
                    placeholder="cn"
                  />
                </div>
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
                    setMappingRows([...mappingRows, { groupDN: "", roleId: "" }]);
                  }}
                >
                  <Plus className="mr-1 h-3 w-3" />
                  Add Mapping
                </Button>
              </div>
              {mappingRows.length === 0 && (
                <p className="text-sm text-muted-foreground">
                  No mappings configured. LDAP users will be assigned the
                  default role.
                </p>
              )}
              <div className="space-y-2">
                {mappingRows.map((row, idx) => (
                  <div key={idx} className="flex items-center gap-2">
                    <Input
                      className="flex-1"
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

            {/* Default role and sync interval */}
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
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
              <div className="space-y-2">
                <Label>Sync Interval (minutes)</Label>
                <Input
                  type="number"
                  min={1}
                  value={form.sync_interval_minutes}
                  onChange={(e) => {
                    setForm({
                      ...form,
                      sync_interval_minutes: parseInt(e.target.value, 10) || 60,
                    });
                  }}
                />
              </div>
            </div>

            {/* Test connection */}
            {!isNew && editingId && (
              <div className="rounded-lg border bg-muted/50 p-4">
                <h3 className="mb-3 font-medium">Test Connection</h3>
                <div className="flex items-center gap-2">
                  <Input
                    className="max-w-xs"
                    value={testUsername}
                    onChange={(e) => { setTestUsername(e.target.value); }}
                    placeholder="Test username (optional)"
                  />
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
                    Test
                  </Button>
                  <Button
                    variant="outline"
                    onClick={handleSync}
                    disabled={syncLDAP.isPending}
                  >
                    {syncLDAP.isPending ? (
                      <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                    ) : (
                      <RefreshCw className="mr-2 h-4 w-4" />
                    )}
                    Sync Now
                  </Button>
                </div>
                {testResult && (
                  <div
                    className={`mt-2 flex items-center gap-2 text-sm ${testResult.success ? "text-green-600" : "text-destructive"}`}
                  >
                    {testResult.success ? (
                      <CheckCircle2 className="h-4 w-4" />
                    ) : (
                      <XCircle className="h-4 w-4" />
                    )}
                    {testResult.message}
                  </div>
                )}
                {syncResult && (
                  <p className="mt-2 text-sm text-muted-foreground">
                    {syncResult}
                  </p>
                )}
              </div>
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
