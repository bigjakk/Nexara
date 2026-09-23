import { useRef } from "react";
import { AlertTriangle, Lock, Upload } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  pbsKeyStatus,
  pbsKeyTextError,
  type PBSEncryptionChoice,
  type PBSEncryptionMode,
} from "../lib/pbs-encryption";

interface RadioOptionProps {
  name: string;
  label: string;
  checked: boolean;
  onSelect: () => void;
}

function RadioOption({ name, label, checked, onSelect }: RadioOptionProps) {
  return (
    <label className="flex items-center gap-2 text-sm">
      <input
        type="radio"
        name={name}
        checked={checked}
        onChange={onSelect}
        className="h-4 w-4 accent-primary"
      />
      {label}
    </label>
  );
}

interface PBSEncryptionFieldProps {
  /** Prefix for element ids and radio names, unique per dialog. */
  idPrefix: string;
  /** The Proxmox storage id, which the warning names. */
  storage: string;
  /**
   * The storage's encryption-key as the config read returns it — a
   * fingerprint, or 1 for a key recorded without one. Undefined when the
   * storage has no key, and in the add dialog.
   */
  current?: string | undefined;
  value: PBSEncryptionChoice;
  onChange: (next: PBSEncryptionChoice) => void;
}

/**
 * The PBS client-encryption choice, laid out the way the Proxmox GUI's
 * Encryption tab lays it out (PBSEncryptionKeyTab in pve-manager
 * www/manager6/storage/PBSEdit.js).
 *
 * A storage with a key shows it as a status line — the fingerprint is all the
 * config read has, and Proxmox would refuse it as a key — and offers Keep (the
 * default, which sends nothing), Replace, or Remove. Without one: Do not
 * encrypt (the default), auto-generate, or an existing key. Replacing and
 * removing both carry the warning the GUI gives: backups made with the current
 * key can only ever be restored with it.
 */
export function PBSEncryptionField({
  idPrefix,
  storage,
  current,
  value,
  onChange,
}: PBSEncryptionFieldProps) {
  const fileInput = useRef<HTMLInputElement>(null);
  const status = pbsKeyStatus(current);
  const hasKey = status !== null;
  const replacing =
    hasKey && (value.mode === "autogen" || value.mode === "existing");
  const keyError =
    value.mode === "existing" && value.keyText !== ""
      ? pbsKeyTextError(value.keyText)
      : null;
  const keyPath = `/etc/pve/priv/storage/${storage}.enc`;

  function select(mode: PBSEncryptionMode) {
    onChange({ ...value, mode });
  }

  return (
    <fieldset className="space-y-2">
      <legend className="mb-1.5 text-sm font-medium leading-none">
        Encryption
      </legend>

      {status && (
        <p className="flex items-center gap-1.5 text-sm">
          <Lock className="h-3.5 w-3.5 shrink-0 text-emerald-600" />
          {status.shortFingerprint ? (
            <span>
              Encryption enabled — fingerprint{" "}
              <code
                className="font-mono text-xs"
                title={status.fingerprint ?? undefined}
              >
                {status.shortFingerprint}
              </code>
            </span>
          ) : (
            <span>Encryption enabled</span>
          )}
        </p>
      )}

      <div className="space-y-1.5">
        {hasKey ? (
          <>
            <RadioOption
              name={`${idPrefix}-pbs-encryption`}
              label="Keep the current key"
              checked={value.mode === "keep"}
              onSelect={() => {
                select("keep");
              }}
            />
            <RadioOption
              name={`${idPrefix}-pbs-encryption`}
              label="Replace the key"
              checked={replacing}
              onSelect={() => {
                select("autogen");
              }}
            />
            {replacing && (
              <fieldset className="space-y-1.5 pl-6">
                <legend className="sr-only">New key</legend>
                <RadioOption
                  name={`${idPrefix}-pbs-new-key`}
                  label="Auto-generate a new key"
                  checked={value.mode === "autogen"}
                  onSelect={() => {
                    select("autogen");
                  }}
                />
                <RadioOption
                  name={`${idPrefix}-pbs-new-key`}
                  label="Use an existing key"
                  checked={value.mode === "existing"}
                  onSelect={() => {
                    select("existing");
                  }}
                />
              </fieldset>
            )}
            <RadioOption
              name={`${idPrefix}-pbs-encryption`}
              label="Remove the key"
              checked={value.mode === "remove"}
              onSelect={() => {
                select("remove");
              }}
            />
          </>
        ) : (
          <>
            <RadioOption
              name={`${idPrefix}-pbs-encryption`}
              label="Do not encrypt"
              checked={value.mode === "none"}
              onSelect={() => {
                select("none");
              }}
            />
            <RadioOption
              name={`${idPrefix}-pbs-encryption`}
              label="Auto-generate a key"
              checked={value.mode === "autogen"}
              onSelect={() => {
                select("autogen");
              }}
            />
            <RadioOption
              name={`${idPrefix}-pbs-encryption`}
              label="Use an existing key"
              checked={value.mode === "existing"}
              onSelect={() => {
                select("existing");
              }}
            />
          </>
        )}
      </div>

      {value.mode === "existing" && (
        <div className="space-y-1.5 pl-6">
          <Label htmlFor={`${idPrefix}-pbs-key`}>Key</Label>
          <Textarea
            id={`${idPrefix}-pbs-key`}
            value={value.keyText}
            onChange={(e) => {
              onChange({ ...value, keyText: e.target.value });
            }}
            rows={4}
            spellCheck={false}
            autoComplete="off"
            aria-invalid={keyError !== null}
            className="font-mono text-xs"
            placeholder="Paste the key file's contents"
          />
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              fileInput.current?.click();
            }}
          >
            <Upload className="mr-1 h-3.5 w-3.5" />
            Load from file
          </Button>
          <input
            ref={fileInput}
            type="file"
            aria-label="Key file"
            accept=".json,.enc,.key,application/json,text/plain"
            className="hidden"
            onChange={(e) => {
              const file = e.target.files?.[0];
              // Cleared so that choosing the same file again still fires.
              e.target.value = "";
              if (!file) return;
              void file.text().then((text) => {
                onChange({ mode: "existing", keyText: text });
              });
            }}
          />
          <p className="text-xs text-muted-foreground">
            The JSON key file proxmox-backup-client writes. Nexara passes it to
            Proxmox and keeps no copy.
          </p>
          {keyError && <p className="text-xs text-destructive">{keyError}</p>}
        </div>
      )}

      {value.mode === "autogen" && (
        <p className="text-xs text-muted-foreground">
          Proxmox generates the key when you save, and Nexara then shows it to
          you once so that you can keep a copy.
        </p>
      )}

      {(replacing || value.mode === "remove") && (
        <div className="flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-950/20">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />
          <p className="text-sm text-amber-800 dark:text-amber-200">
            {value.mode === "remove"
              ? "Backups made from now on will not be encrypted. "
              : "Backups made from now on will be encrypted with the new key. "}
            Backups already made with the current key can only be restored with
            the current key, so keep a copy of it before you continue. If you do
            not have one, it is at{" "}
            <code className="font-mono text-xs break-all">{keyPath}</code> on
            any node of this cluster.
          </p>
        </div>
      )}
    </fieldset>
  );
}
