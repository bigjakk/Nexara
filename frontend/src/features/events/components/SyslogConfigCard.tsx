import { useEffect, useState } from "react";
import { Loader2, CheckCircle2, XCircle, Settings2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  useSyslogConfig,
  useSaveSyslogConfig,
  useTestSyslog,
  type SyslogConfig,
} from "../api/events-queries";

const inputClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

const facilities = [
  { value: 0, label: "kern (0)" },
  { value: 1, label: "user (1)" },
  { value: 2, label: "mail (2)" },
  { value: 3, label: "daemon (3)" },
  { value: 4, label: "auth (4)" },
  { value: 5, label: "syslog (5)" },
  { value: 10, label: "authpriv (10)" },
  { value: 16, label: "local0 (16)" },
  { value: 17, label: "local1 (17)" },
  { value: 18, label: "local2 (18)" },
  { value: 19, label: "local3 (19)" },
  { value: 20, label: "local4 (20)" },
  { value: 21, label: "local5 (21)" },
  { value: 22, label: "local6 (22)" },
  { value: 23, label: "local7 (23)" },
] as const;

export function SyslogConfigCard() {
  const { data: savedConfig, isLoading } = useSyslogConfig();
  const saveMutation = useSaveSyslogConfig();
  const testMutation = useTestSyslog();

  const [config, setConfig] = useState<SyslogConfig>({
    enabled: false,
    host: "",
    port: 514,
    protocol: "udp",
    facility: 16,
    tls_skip_verify: false,
  });
  const [expanded, setExpanded] = useState(false);

  useEffect(() => {
    if (savedConfig) {
      setConfig(savedConfig);
      if (savedConfig.enabled) {
        setExpanded(true);
      }
    }
  }, [savedConfig]);

  const handleSave = () => {
    saveMutation.mutate(config);
  };

  const handleTest = () => {
    testMutation.mutate(config);
  };

  const update = (partial: Partial<SyslogConfig>) => {
    setConfig((prev) => ({ ...prev, ...partial }));
  };

  if (isLoading) return null;

  return (
    <div className="rounded-md border">
      <button
        className="flex w-full items-center justify-between px-4 py-3 text-left hover:bg-muted/20"
        onClick={() => { setExpanded(!expanded); }}
      >
        <div className="flex items-center gap-2">
          <Settings2 className="h-4 w-4 text-muted-foreground" />
          <span className="text-sm font-medium">Syslog Forwarding</span>
          {config.enabled && (
            <span className="inline-flex items-center rounded-full bg-green-500/10 px-2 py-0.5 text-xs font-medium text-green-600 dark:text-green-400">
              Active
            </span>
          )}
        </div>
        <span className="text-xs text-muted-foreground">
          {expanded ? "Hide" : "Configure"}
        </span>
      </button>

      {expanded && (
        <div className="border-t px-4 py-4 space-y-4">
          {/* Enable toggle */}
          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={config.enabled}
              onChange={(e) => { update({ enabled: e.target.checked }); }}
              className="h-4 w-4 rounded border-input"
            />
            <span className="text-sm">Enable syslog forwarding</span>
          </label>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <label className="mb-1 block text-xs text-muted-foreground">Host</label>
              <input
                type="text"
                className={inputClass}
                placeholder="syslog.example.com"
                value={config.host}
                onChange={(e) => { update({ host: e.target.value }); }}
              />
            </div>

            <div>
              <label className="mb-1 block text-xs text-muted-foreground">Port</label>
              <input
                type="number"
                className={inputClass}
                value={config.port}
                min={1}
                max={65535}
                onChange={(e) => { update({ port: parseInt(e.target.value, 10) || 514 }); }}
              />
            </div>

            <div>
              <label className="mb-1 block text-xs text-muted-foreground">Protocol</label>
              <select
                className={selectClass}
                value={config.protocol}
                onChange={(e) => { update({ protocol: e.target.value }); }}
              >
                <option value="udp">UDP</option>
                <option value="tcp">TCP</option>
                <option value="tls">TLS</option>
              </select>
            </div>

            <div>
              <label className="mb-1 block text-xs text-muted-foreground">Facility</label>
              <select
                className={selectClass}
                value={config.facility}
                onChange={(e) => { update({ facility: parseInt(e.target.value, 10) }); }}
              >
                {facilities.map((f) => (
                  <option key={f.value} value={f.value}>
                    {f.label}
                  </option>
                ))}
              </select>
            </div>
          </div>

          {config.protocol === "tls" && (
            <label className="flex items-center gap-2 cursor-pointer">
              <input
                type="checkbox"
                checked={config.tls_skip_verify}
                onChange={(e) => { update({ tls_skip_verify: e.target.checked }); }}
                className="h-4 w-4 rounded border-input"
              />
              <span className="text-sm">Skip TLS certificate verification</span>
            </label>
          )}

          {/* Actions */}
          <div className="flex items-center gap-3">
            <Button size="sm" onClick={handleSave} disabled={saveMutation.isPending}>
              {saveMutation.isPending && <Loader2 className="mr-1 h-3 w-3 animate-spin" />}
              Save
            </Button>

            <Button
              variant="outline"
              size="sm"
              onClick={handleTest}
              disabled={testMutation.isPending || !config.host}
            >
              {testMutation.isPending && <Loader2 className="mr-1 h-3 w-3 animate-spin" />}
              Test Connection
            </Button>

            {/* Status feedback */}
            {saveMutation.isSuccess && (
              <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                <CheckCircle2 className="h-3 w-3" />
                Saved
              </span>
            )}
            {saveMutation.isError && (
              <span className="flex items-center gap-1 text-xs text-red-500">
                <XCircle className="h-3 w-3" />
                {saveMutation.error instanceof Error ? saveMutation.error.message : "Failed to save"}
              </span>
            )}
            {testMutation.isSuccess && (
              <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                <CheckCircle2 className="h-3 w-3" />
                Test message sent
              </span>
            )}
            {testMutation.isError && (
              <span className="flex items-center gap-1 text-xs text-red-500">
                <XCircle className="h-3 w-3" />
                {testMutation.error instanceof Error ? testMutation.error.message : "Test failed"}
              </span>
            )}
          </div>

          <p className="text-xs text-muted-foreground">
            When enabled, all audit events are forwarded in real-time to the configured syslog server
            using RFC 5424 format. Severity is derived from the action type.
          </p>
        </div>
      )}
    </div>
  );
}
