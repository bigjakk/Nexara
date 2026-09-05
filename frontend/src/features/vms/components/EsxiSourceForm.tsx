import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { useRegisterEsxiSource } from "../api/import-queries";

interface EsxiSourceFormProps {
  clusterId: string;
  onRegistered: (storage: string) => void;
  onCancel: () => void;
}

// PVE storage IDs: start with a letter, then letters/digits/-_. (no spaces or slashes).
const storageIdPattern = /^[A-Za-z][A-Za-z0-9\-_.]*$/;

export function EsxiSourceForm({
  clusterId,
  onRegistered,
  onCancel,
}: EsxiSourceFormProps) {
  const [storage, setStorage] = useState("");
  const [server, setServer] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [skipCert, setSkipCert] = useState(false);
  const register = useRegisterEsxiSource();

  const storageValid = storageIdPattern.test(storage) && storage.length <= 64;
  const canSubmit =
    storageValid &&
    server.trim() !== "" &&
    username.trim() !== "" &&
    password !== "";

  function submit() {
    if (!canSubmit) return;
    register.mutate(
      {
        clusterId,
        body: {
          storage: storage.trim(),
          server: server.trim(),
          username: username.trim(),
          password,
          skip_cert_verification: skipCert,
        },
      },
      {
        onSuccess: () => {
          onRegistered(storage.trim());
        },
      },
    );
  }

  return (
    <div className="space-y-3 rounded-md border border-border p-3">
      <div className="text-sm font-medium">Add ESXi / vCenter source</div>
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1">
          <Label>Storage ID</Label>
          <Input
            value={storage}
            onChange={(e) => {
              setStorage(e.target.value);
            }}
            placeholder="esxi-prod"
            autoComplete="off"
            spellCheck={false}
          />
          {storage !== "" && !storageValid && (
            <p className="text-xs text-destructive">
              Must start with a letter; letters, digits, <code>-_.</code> only.
            </p>
          )}
        </div>
        <div className="space-y-1">
          <Label>Server</Label>
          <Input
            value={server}
            onChange={(e) => {
              setServer(e.target.value);
            }}
            placeholder="esxi.example.com"
            autoComplete="off"
            spellCheck={false}
          />
        </div>
        <div className="space-y-1">
          <Label>Username</Label>
          <Input
            value={username}
            onChange={(e) => {
              setUsername(e.target.value);
            }}
            placeholder="root"
            autoComplete="off"
            spellCheck={false}
          />
        </div>
        <div className="space-y-1">
          <Label>Password</Label>
          <Input
            type="password"
            value={password}
            onChange={(e) => {
              setPassword(e.target.value);
            }}
            autoComplete="off"
          />
        </div>
      </div>
      <label className="flex items-center gap-2 text-sm">
        <Checkbox
          checked={skipCert}
          onCheckedChange={(v) => {
            setSkipCert(v === true);
          }}
        />
        <span>Skip TLS certificate verification (self-signed ESXi hosts)</span>
      </label>
      {register.isError && (
        <p className="text-sm text-destructive">
          {register.error instanceof Error
            ? register.error.message
            : "Failed to register source"}
        </p>
      )}
      <div className="flex gap-2">
        <Button
          type="button"
          size="sm"
          disabled={!canSubmit || register.isPending}
          onClick={submit}
        >
          {register.isPending ? "Registering…" : "Register source"}
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={onCancel}
          disabled={register.isPending}
        >
          Cancel
        </Button>
      </div>
    </div>
  );
}
