import { useEffect, useState } from "react";
import {
  Loader2,
  CheckCircle2,
  XCircle,
  Settings2,
  AlertTriangle,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import { ApiClientError } from "@/lib/api-client";
import {
  useSyslogConfig,
  useSaveSyslogConfig,
  useTestSyslog,
  type SyslogConfig,
} from "../api/events-queries";

const inputClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring";

interface FacilityOption {
  value: number;
  label: string;
}

// kern (0) is not offered. The API reads facility 0 as "not set" and stores 16
// (local0) in its place — applySyslogDefaults in internal/api/handlers/audit.go
// — so an operator who picked kern got local0.
const facilities: readonly FacilityOption[] = [
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
];

/**
 * The facility select's options while the form holds `current` and the saved
 * config `saved`. The API stores any facility from 1 to 23, so a config written
 * through it directly can hold one this list does not name, and that one gets
 * an option of its own — kept after the operator picks another, so it can be
 * picked back. Without it React falls back to selecting the first option: the
 * select would show user (1) while Save sent the saved number back.
 *
 * `current` is not redundant with `saved`: in the render between a refetch and
 * the load effect, the form still holds the old facility while `saved` is
 * already the new one.
 */
function facilityOptions(
  current: number,
  saved?: number,
): readonly FacilityOption[] {
  const unlisted = new Set(
    [current, saved].filter(
      (v): v is number =>
        v !== undefined && !facilities.some((f) => f.value === v),
    ),
  );
  if (unlisted.size === 0) return facilities;
  return [
    ...facilities,
    ...[...unlisted].map((v) => ({ value: v, label: `other (${String(v)})` })),
  ].sort((a, b) => a.value - b.value);
}

/**
 * The port the API substitutes for port 0: 6514 for tls (RFC 5425), 514
 * otherwise — applySyslogDefaults in internal/api/handlers/audit.go. That match
 * is case-insensitive; this one need not be, because the form's protocol is
 * always lower-case: the select offers only lower-case values, and formConfig
 * lower-cases a loaded one.
 */
function defaultSyslogPort(protocol: string): number {
  return protocol === "tls" ? 6514 : 514;
}

/**
 * A saved config as the form holds it — what Save would store, rather than the
 * row as read. The protocol is lower-cased: the API lower-cases it only when it
 * saves an ENABLED config (UpdateSyslogConfig, internal/api/handlers/audit.go),
 * so a disabled one can hold "TLS", which none of the select's options match.
 * And facility 0 reads as 16 (local0): the API replaces a 0 with 16 before it
 * stores the config (applySyslogDefaults), and the forwarder sends a 0 as 16
 * (formatRFC5424 and Test in internal/syslog/forwarder.go). No API write leaves
 * a 0 behind, but a row written some other way can hold one.
 */
function formConfig(saved: SyslogConfig): SyslogConfig {
  return {
    ...saved,
    protocol: saved.protocol.toLowerCase(),
    facility: saved.facility === 0 ? 16 : saved.facility,
  };
}

/**
 * What a failed Test says. TestSyslog (internal/api/handlers/audit.go) answers
 * a probe that could not connect with 400 {success: false, error: "<reason>"}:
 * the reason is in `error`, and there is no `message`, which is where the API
 * client takes an error's text from — so that text is empty, and the reason is
 * read from the body instead. Other API failures carry the standard {error,
 * message} envelope, whose message is the text to show and whose `error` is
 * only a status slug.
 */
function testFailure(error: Error): string {
  if (error.message !== "") return error.message;
  if (error instanceof ApiClientError && error.body.error !== "") {
    return error.body.error;
  }
  return "Test failed";
}

/**
 * Protocols the select does not offer, but the form or the saved config holds.
 * The API checks the protocol only when it saves an enabled config, so a
 * disabled one can be stored with a protocol the select does not offer
 * ("sctp", say). Like an unlisted facility, it gets an option of its own, or
 * React would select UDP while Save sent the stored value back.
 */
function unlistedProtocols(current: string, saved: string): string[] {
  return [...new Set([current, saved])].filter(
    (p) => p !== "udp" && p !== "tcp" && p !== "tls",
  );
}

/** The port field's message, named by the field's aria-describedby. */
const portErrorId = "syslog-port-error";

export function SyslogConfigCard() {
  const syslogQuery = useSyslogConfig();
  const savedConfig = syslogQuery.data;
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
  // Whether the port is the operator's own. Until it is, the field holds the
  // protocol's default and a protocol switch moves it along: 514 for udp and
  // tcp, 6514 for tls. Chosen means typed into the field since the saved
  // config last changed — clearing it included — or loaded as anything but
  // the saved protocol's default: a saved 1514 survives a switch, while a
  // saved 514 on udp is only the default and becomes 6514 on a switch to tls.
  const [portChosen, setPortChosen] = useState(false);
  // Whether the port field holds text it cannot read as a number. Such a field
  // reports a value of "", exactly as a blank one does, so without this Save
  // and Test would send port 0 — the protocol's default — while the field
  // showed something else.
  const [portInvalid, setPortInvalid] = useState(false);
  const [expanded, setExpanded] = useState(false);

  useEffect(() => {
    if (savedConfig) {
      const loaded = formConfig(savedConfig);
      setConfig(loaded);
      setPortChosen(loaded.port !== defaultSyslogPort(loaded.protocol));
      // The saved port replaces whatever the field held.
      setPortInvalid(false);
      if (loaded.enabled) {
        setExpanded(true);
      }
    }
  }, [savedConfig]);

  // What is wrong with the port, if anything; Save and Test hold while it is
  // set. The range is checked on the form's port rather than on the field's
  // validity, because React rewrites the field after some edits — 0 to blank,
  // 1e3 and 1.5 to 1 — and a validity read at input time would describe text
  // that is gone. Blank (port 0) passes: it is the API's "no port".
  const portError = portInvalid
    ? "Port must be a number"
    : config.port !== 0 && (config.port < 1 || config.port > 65535)
      ? "Port must be between 1 and 65535"
      : null;
  // A save that stored the config but could not connect answers with a
  // warning in place of the config; the key is what tells the two apart.
  const reply = saveMutation.data;
  const warning =
    reply !== undefined && "warning" in reply ? reply.warning : null;

  const handleSave = () => {
    saveMutation.mutate(config);
  };

  const handleTest = () => {
    testMutation.mutate(config);
  };

  const update = (partial: Partial<SyslogConfig>) => {
    setConfig((prev) => ({ ...prev, ...partial }));
  };

  if (savedConfig === undefined) {
    // The first read shows nothing yet. A read that has failed before and is
    // being retried is not a first read: its failure stays on screen.
    if (syslogQuery.isLoading && syslogQuery.errorUpdatedAt === 0) return null;
    // Any other state without a saved config — a failed read, a 403, a read
    // TanStack has paused (offline, or retrying in a background tab) — gets a
    // notice in place of the form. The form would hold its built-in defaults
    // (off, udp, 514): to a reader that says forwarding is off, and Save would
    // write those defaults over a live config the card never read.
    return (
      <div className="rounded-md border">
        <div className="flex items-center gap-2 px-4 py-3">
          <Settings2 className="h-4 w-4 text-muted-foreground" />
          <span className="text-sm font-medium">Syslog Forwarding</span>
        </div>
        <div className="border-t px-4 py-4">
          <QueryStateNotice
            query={syslogQuery}
            subject="the syslog forwarding settings"
            empty="Nexara returned no syslog forwarding settings."
          />
        </div>
      </div>
    );
  }

  return (
    <div className="rounded-md border">
      <button
        className="flex w-full items-center justify-between px-4 py-3 text-left hover:bg-muted/20"
        onClick={() => {
          // Collapsing unmounts the port field and any text in it. The field
          // reopens from the form's port, which unreadable text left at 0 —
          // blank — so an error about that text must not outlive it.
          if (expanded) setPortInvalid(false);
          setExpanded(!expanded);
        }}
      >
        <div className="flex items-center gap-2">
          <Settings2 className="h-4 w-4 text-muted-foreground" />
          <span className="text-sm font-medium">Syslog Forwarding</span>
          {config.enabled && (
            <span className="inline-flex items-center rounded-full bg-emerald-500/10 px-2 py-0.5 text-xs font-medium text-emerald-600 dark:text-emerald-400">
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
              onChange={(e) => {
                update({ enabled: e.target.checked });
              }}
              className="h-4 w-4 rounded border-input"
            />
            <span className="text-sm">Enable syslog forwarding</span>
          </label>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <label
                htmlFor="syslog-host"
                className="mb-1 block text-xs text-muted-foreground"
              >
                Host
              </label>
              <input
                id="syslog-host"
                type="text"
                className={inputClass}
                placeholder="syslog.example.com"
                value={config.host}
                onChange={(e) => {
                  update({ host: e.target.value });
                }}
              />
            </div>

            <div>
              <label
                htmlFor="syslog-port"
                className="mb-1 block text-xs text-muted-foreground"
              >
                Port
              </label>
              <input
                id="syslog-port"
                type="number"
                className={inputClass}
                value={config.port === 0 ? "" : config.port}
                placeholder={String(defaultSyslogPort(config.protocol))}
                min={1}
                max={65535}
                aria-invalid={portError !== null}
                aria-describedby={portError !== null ? portErrorId : undefined}
                onChange={(e) => {
                  // A blank field is kept as port 0 rather than refilled. 0 is
                  // the API's "no port": syslogConfigParams declares it
                  // (internal/api/registry_audit.go), and applySyslogDefaults
                  // (internal/api/handlers/audit.go) turns it into the
                  // protocol's default for Save and Test alike. The
                  // placeholder shows which default that will be.
                  const port = parseInt(e.target.value, 10);
                  setPortChosen(true);
                  update({ port: Number.isNaN(port) ? 0 : port });
                }}
                onInput={(e) => {
                  // Unreadable text reaches onChange as a blank field, and
                  // not at all when the field was blank already — React only
                  // reports a change of value, and "" to "" is none. onInput
                  // fires on every edit, so the check lives here.
                  setPortInvalid(e.currentTarget.validity.badInput);
                }}
              />
              {portError !== null && (
                <p
                  id={portErrorId}
                  role="alert"
                  className="mt-1 text-xs text-red-500"
                >
                  {portError}
                </p>
              )}
            </div>

            <div>
              <label
                htmlFor="syslog-protocol"
                className="mb-1 block text-xs text-muted-foreground"
              >
                Protocol
              </label>
              <select
                id="syslog-protocol"
                className={selectClass}
                value={config.protocol}
                onChange={(e) => {
                  const protocol = e.target.value;
                  setConfig((prev) => ({
                    ...prev,
                    protocol,
                    port: portChosen ? prev.port : defaultSyslogPort(protocol),
                  }));
                }}
              >
                <option value="udp">UDP</option>
                <option value="tcp">TCP</option>
                <option value="tls">TLS</option>
                {unlistedProtocols(
                  config.protocol,
                  formConfig(savedConfig).protocol,
                ).map((p) => (
                  <option key={p} value={p}>
                    {`other (${p})`}
                  </option>
                ))}
              </select>
            </div>

            <div>
              <label
                htmlFor="syslog-facility"
                className="mb-1 block text-xs text-muted-foreground"
              >
                Facility
              </label>
              <select
                id="syslog-facility"
                className={selectClass}
                value={config.facility}
                onChange={(e) => {
                  update({ facility: parseInt(e.target.value, 10) });
                }}
              >
                {facilityOptions(
                  config.facility,
                  formConfig(savedConfig).facility,
                ).map((f) => (
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
                onChange={(e) => {
                  update({ tls_skip_verify: e.target.checked });
                }}
                className="h-4 w-4 rounded border-input"
              />
              <span className="text-sm">Skip TLS certificate verification</span>
            </label>
          )}

          {/* Actions */}
          <div className="flex items-center gap-3">
            <Button
              size="sm"
              onClick={handleSave}
              disabled={saveMutation.isPending || portError !== null}
            >
              {saveMutation.isPending && (
                <Loader2 className="mr-1 h-3 w-3 animate-spin" />
              )}
              Save
            </Button>

            <Button
              variant="outline"
              size="sm"
              onClick={handleTest}
              disabled={
                testMutation.isPending || !config.host || portError !== null
              }
            >
              {testMutation.isPending && (
                <Loader2 className="mr-1 h-3 w-3 animate-spin" />
              )}
              Test Connection
            </Button>

            {/* Status feedback */}
            <div role="status" className="flex items-center gap-3">
              {saveMutation.isSuccess && warning === null && (
                <span className="flex items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400">
                  <CheckCircle2 className="h-3 w-3" />
                  Saved
                </span>
              )}
              {/* Stored, but the forwarder could not connect. It tries again
                  for each later record and drops any it cannot deliver,
                  which a plain "Saved" would hide. */}
              {saveMutation.isSuccess && warning !== null && (
                <span className="flex items-center gap-1 text-xs text-amber-600 dark:text-amber-400">
                  <AlertTriangle className="h-3 w-3" />
                  {warning}
                </span>
              )}
              {saveMutation.isError && (
                <span className="flex items-center gap-1 text-xs text-red-500">
                  <XCircle className="h-3 w-3" />
                  {saveMutation.error instanceof Error
                    ? saveMutation.error.message
                    : "Failed to save"}
                </span>
              )}
              {testMutation.isSuccess && (
                <span className="flex items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400">
                  <CheckCircle2 className="h-3 w-3" />
                  Test message sent
                </span>
              )}
              {testMutation.isError && (
                <span className="flex items-center gap-1 text-xs text-red-500">
                  <XCircle className="h-3 w-3" />
                  {testMutation.error instanceof Error
                    ? testFailure(testMutation.error)
                    : "Test failed"}
                </span>
              )}
            </div>
          </div>

          <p className="text-xs text-muted-foreground">
            When enabled, all audit events are forwarded in real-time to the
            configured syslog server using RFC 5424 format. Severity is derived
            from the action type.
          </p>
        </div>
      )}
    </div>
  );
}
