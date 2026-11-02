import { useState, type ReactNode } from "react";
import { CheckCircle2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { PrivateAddressWarning } from "@/components/PrivateAddressWarning";
import { ConfirmRequiredWarning } from "@/components/ConfirmRequiredWarning";
import { privateAddressWarningFromError } from "@/lib/private-address";
import { confirmRequiredFromError } from "@/lib/confirm-gate";
import { describeError } from "@/lib/api-error";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { useCreateNutServer, useProbeNutServer, useUpdateNutServer } from "../api/ups-queries";
import type { NutProbeResult, NutServer, NutServerRequest } from "../types/ups";

const CONFIRM_CODES = [
  "nut_cleartext_credentials_confirm_required",
  "nut_insecure_tls_confirm_required",
] as const;

type Acks = Pick<
  NutServerRequest,
  "allow_private_address" | "acknowledge_cleartext_credentials" | "acknowledge_insecure_tls"
>;

interface NutServerDialogProps {
  /** Editing this server; omitted to add one. */
  server?: NutServer;
  trigger?: ReactNode;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}

function errorText(err: unknown): string {
  // describeError gives "HTTP 429" for a body-less answer, and nothing for a
  // request that never got one.
  return describeError(err) || "The request failed — check your connection and try again.";
}

/**
 * Add or edit a NUT server. Saving probes the server first, so an address
 * that does not answer is refused rather than stored. A private address, or
 * a password that would travel without TLS, is a warning to confirm, not a
 * refusal: a NUT server on a LAN without TLS is the usual case.
 */
export function NutServerDialog({ server, trigger, open: openProp, onOpenChange }: NutServerDialogProps) {
  const editing = server !== undefined;
  const [openState, setOpenState] = useState(false);
  const open = openProp ?? openState;
  const [name, setName] = useState(server?.name ?? "");
  const [host, setHost] = useState(server?.host ?? "");
  const [port, setPort] = useState(String(server?.port ?? 3493));
  const [username, setUsername] = useState(server?.username ?? "");
  const [password, setPassword] = useState("");
  const [clearCredentials, setClearCredentials] = useState(false);
  const [tlsMode, setTlsMode] = useState<"off" | "starttls">(server?.tls_mode ?? "off");
  const [verifyTls, setVerifyTls] = useState(server?.verify_tls ?? true);
  const [fingerprint, setFingerprint] = useState(server?.tls_fingerprint ?? "");
  const [enabled, setEnabled] = useState(server?.enabled ?? true);
  const [acks, setAcks] = useState<Acks>({});
  const [probe, setProbe] = useState<NutProbeResult | null>(null);

  const create = useCreateNutServer();
  const update = useUpdateNutServer();
  const probeServer = useProbeNutServer();
  const save = editing ? update : create;
  // The add dialog stays mounted and toggles; the edit one is mounted only
  // while it is open, so its open flag is always true.
  const settle = useSaveOutcome(open);
  const lastError = save.error ?? probeServer.error;
  const privateWarning = privateAddressWarningFromError(lastError);
  const confirm = confirmRequiredFromError(lastError, CONFIRM_CODES, null);

  function setOpen(next: boolean) {
    if (openProp === undefined) setOpenState(next);
    onOpenChange?.(next);
    if (!next && !editing) {
      setName("");
      setHost("");
      setPort("3493");
      setUsername("");
      setPassword("");
      setTlsMode("off");
      setVerifyTls(true);
      setFingerprint("");
      setAcks({});
      setProbe(null);
      create.reset();
      probeServer.reset();
    }
  }

  function body(extra: Acks): NutServerRequest {
    const req: NutServerRequest = {
      host,
      port: Number(port),
      tls_mode: tlsMode,
      verify_tls: verifyTls,
      tls_fingerprint: tlsMode === "starttls" ? fingerprint : "",
      ...acks,
      ...extra,
    };
    if (clearCredentials) {
      req.clear_credentials = true;
    } else {
      req.username = username;
      if (password !== "") req.password = password;
    }
    return req;
  }

  function submit(extra: Acks = {}) {
    setAcks((a) => ({ ...a, ...extra }));
    const req = { ...body(extra), name };
    settle(editing ? update.mutateAsync({ ...req, enabled, id: server.id }) : create.mutateAsync(req), {
      // Named: a late failure is toasted, perhaps over another page.
      action: editing ? `Saving NUT server ${server.name}` : `Adding NUT server ${name}`,
      onSuccess: () => { setOpen(false); },
      // Shown below from the mutation's own state: a prompt for a refusal
      // that needs confirming, or the error itself.
      onError: () => undefined,
    });
  }

  function test(extra: Acks = {}) {
    setAcks((a) => ({ ...a, ...extra }));
    setProbe(null);
    probeServer.mutate(body(extra), { onSuccess: setProbe });
  }

  function resend(extra: Acks) {
    if (probeServer.isError && !save.isError) test(extra);
    else submit(extra);
  }

  const pending = save.isPending || probeServer.isPending;

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {trigger !== undefined && <DialogTrigger asChild>{trigger}</DialogTrigger>}
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{editing ? `Edit ${server.name}` : "Add NUT server"}</DialogTitle>
          <DialogDescription>
            Nexara connects to upsd on TCP and reads every UPS that server reports.
          </DialogDescription>
        </DialogHeader>

        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <div className="space-y-2">
            <Label htmlFor="nut-name">Name</Label>
            <Input id="nut-name" value={name} onChange={(e) => { setName(e.target.value); }} required maxLength={64} />
          </div>
          <div className="grid grid-cols-[1fr_7rem] gap-3">
            <div className="space-y-2">
              <Label htmlFor="nut-host">Host</Label>
              <Input id="nut-host" value={host} onChange={(e) => { setHost(e.target.value); }} required placeholder="192.0.2.40" />
            </div>
            <div className="space-y-2">
              <Label htmlFor="nut-port">Port</Label>
              <Input id="nut-port" type="number" min={1} max={65535} value={port} onChange={(e) => { setPort(e.target.value); }} required />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-2">
              <Label htmlFor="nut-username">Username</Label>
              <Input id="nut-username" value={username} disabled={clearCredentials} onChange={(e) => { setUsername(e.target.value); }} placeholder="optional" autoComplete="off" />
            </div>
            <div className="space-y-2">
              <Label htmlFor="nut-password">Password</Label>
              <Input
                id="nut-password"
                type="password"
                value={password}
                disabled={clearCredentials}
                onChange={(e) => { setPassword(e.target.value); }}
                placeholder={editing && server.has_password ? "unchanged" : "optional"}
                autoComplete="new-password"
              />
            </div>
          </div>
          <p className="-mt-2 text-xs text-muted-foreground">
            Only needed where upsd demands an account. Leave both empty and Nexara reads anonymously.
          </p>
          {editing && (server.has_password || server.username !== "") && (
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={clearCredentials} onCheckedChange={(v) => { setClearCredentials(v === true); }} />
              Remove the stored username and password
            </label>
          )}
          <div className="space-y-2">
            <Label htmlFor="nut-tls">Connection security</Label>
            <select
              id="nut-tls"
              className="h-9 w-full rounded-md border bg-background px-3 text-sm"
              value={tlsMode}
              onChange={(e) => { setTlsMode(e.target.value === "starttls" ? "starttls" : "off"); }}
            >
              <option value="off">Plain TCP</option>
              <option value="starttls">STARTTLS (required)</option>
            </select>
          </div>
          {editing && (
            <label className="flex items-start gap-2 text-sm">
              <Checkbox className="mt-0.5" checked={enabled} onCheckedChange={(v) => { setEnabled(v === true); }} />
              <span>
                Poll this server
                <span className="block text-xs text-muted-foreground">
                  Off keeps the server and its history; its UPSes show Polling off and leave the alerts.
                </span>
              </span>
            </label>
          )}
          {tlsMode === "starttls" && (
            <div className="space-y-3 rounded-md border p-3">
              <div className="space-y-2">
                <Label htmlFor="nut-fp">Pinned certificate fingerprint</Label>
                <Input id="nut-fp" value={fingerprint} onChange={(e) => { setFingerprint(e.target.value); }} placeholder="SHA-256, from Test connection" className="font-mono text-xs" />
              </div>
              <label className="flex items-center gap-2 text-sm">
                <Checkbox checked={verifyTls} onCheckedChange={(v) => { setVerifyTls(v === true); }} />
                Verify against the system's trusted CAs when no fingerprint is pinned
              </label>
            </div>
          )}

          {privateWarning !== null && (
            <PrivateAddressWarning
              ip={privateWarning.ip}
              url={host}
              pending={pending}
              onConfirm={() => { resend({ allow_private_address: true }); }}
              onCancel={() => { save.reset(); probeServer.reset(); }}
            />
          )}
          {confirm !== null && (
            <ConfirmRequiredWarning
              title={confirm.code === CONFIRM_CODES[0] ? "Password without TLS" : "Certificate not verified"}
              message={confirm.message}
              confirmLabel="Store it anyway"
              pending={pending}
              onConfirm={() => {
                resend(
                  confirm.code === CONFIRM_CODES[0]
                    ? { acknowledge_cleartext_credentials: true }
                    : { acknowledge_insecure_tls: true },
                );
              }}
              onCancel={() => { save.reset(); probeServer.reset(); }}
            />
          )}
          {lastError !== null && privateWarning === null && confirm === null && (
            <p className="text-sm text-destructive">{errorText(lastError)}</p>
          )}

          {probe !== null && (
            <div className="space-y-1 rounded-md border bg-muted/40 p-3 text-sm">
              <div className="flex items-center gap-2 font-medium">
                <CheckCircle2 className="h-4 w-4 text-emerald-600" aria-hidden="true" />
                Connected in {probe.took_ms} ms · upsd {probe.nut_version || "?"}, protocol {probe.protocol_version || "?"}
              </div>
              <div className="text-muted-foreground">
                {probe.devices.length === 0
                  ? "No UPS configured on this server."
                  : `${String(probe.devices.length)} UPS: ${probe.devices.map((d) => (d.description !== "" ? `${d.name} (${d.description})` : d.name)).join(", ")}`}
              </div>
              {probe.tls && probe.tls_fingerprint !== "" && fingerprint === "" && (
                <Button type="button" size="sm" variant="outline" onClick={() => { setFingerprint(probe.tls_fingerprint); }}>
                  Pin this certificate
                </Button>
              )}
            </div>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => { setOpen(false); }}>
              Cancel
            </Button>
            {/* Add only: an edit with the password left blank would test an
                anonymous connection, not the stored one. Saving re-probes,
                and each table row has its own Test. */}
            {!editing && (
              <Button type="button" variant="outline" disabled={pending || host === ""} onClick={() => { test(); }}>
                {probeServer.isPending ? "Testing…" : "Test connection"}
              </Button>
            )}
            <Button type="submit" disabled={pending || name === "" || host === ""}>
              {save.isPending ? "Saving…" : editing ? "Save" : "Add server"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
