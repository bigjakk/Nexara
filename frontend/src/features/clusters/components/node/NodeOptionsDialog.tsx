import { useId, useState } from "react";
import type { QueryObserverResult } from "@tanstack/react-query";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type {
  NodeOptionReadKey,
  NodeOptions,
} from "../../api/node-options-queries";
import { useNodeOptionsSave } from "../../hooks/useNodeOptionsSave";
import {
  buildLocation,
  buildWakeOnLan,
  changedNodeOptions,
  checkCoordinate,
  checkInteger,
  checkInterfaceName,
  checkIPv4Address,
  checkLocationName,
  checkMacAddress,
  NODE_OPTION_LABELS,
  nodeOptionSupport,
  optionsChangedBetween,
  parseLocation,
  parseWakeOnLan,
  type Checked,
  type NodeOptionDrafts,
} from "../../lib/node-options";
import { ConflictNote } from "./ConflictNote";

/** The text an integer setting starts the form with: nothing, when unset. */
function integerText(value: number | undefined): string {
  return value === undefined ? "" : String(value);
}

/** The message of a check that failed, or null. */
function errorOf<T>(check: Checked<T>): string | null {
  return check.ok ? null : check.error;
}

/** The value of a check that passed, or `fallback`. */
function valueOf<T>(check: Checked<T>, fallback: T): T {
  return check.ok ? check.value : fallback;
}

/**
 * Edits one node's options: the start-on-boot delay, the ballooning target,
 * Wake-on-LAN and the location. Empty settings use Proxmox's defaults, and
 * emptying a field removes the setting.
 *
 * Mounted by the card for ONE open and seeded once from `opened`, the card's
 * data as it was when Edit was pressed, so every open is a fresh instance and
 * nothing a refresh does behind it reaches the form. The form is drawn only
 * from a node that was actually read, and a save carries only the settings the
 * operator changed (changedNodeOptions): sending what the form merely displays
 * writes it back, and what it displays can be out of date.
 *
 * A field a node's version lacks is not drawn, and so never sent; one the read
 * already holds is drawn whatever the version (nodeOptionSupport). The checks on
 * each field are Proxmox's own and block Save, with the reason under the field.
 */
export function EditNodeOptionsDialog({
  clusterId,
  nodeName,
  pveVersion,
  opened,
  reread,
  fallbackFocus,
  onClose,
}: {
  clusterId: string;
  nodeName: string;
  pveVersion: string;
  opened: NodeOptions;
  reread: () => Promise<QueryObserverResult<NodeOptions>>;
  fallbackFocus: () => HTMLElement | null;
  onClose: () => void;
}) {
  const uid = useId();
  const fieldId = (name: string) => `${uid}-${name}`;

  // Which fields this node has, decided once: a version that changes under an
  // open dialog must not make a field appear or go.
  const [support] = useState(() => nodeOptionSupport(pveVersion, opened));
  const wakeOnLan = parseWakeOnLan(opened.wakeonlan ?? "");
  const location = parseLocation(opened.location ?? "");

  const [delay, setDelay] = useState(
    integerText(opened["startall-onboot-delay"]),
  );
  const [target, setTarget] = useState(
    integerText(opened["ballooning-target"]),
  );
  const [mac, setMac] = useState(wakeOnLan.ok ? wakeOnLan.fields.mac : "");
  const [bindInterface, setBindInterface] = useState(
    wakeOnLan.ok ? wakeOnLan.fields.bindInterface : "",
  );
  const [broadcast, setBroadcast] = useState(
    wakeOnLan.ok ? wakeOnLan.fields.broadcastAddress : "",
  );
  const [latitude, setLatitude] = useState(
    location.ok ? location.fields.latitude : "",
  );
  const [longitude, setLongitude] = useState(
    location.ok ? location.fields.longitude : "",
  );
  const [locationName, setLocationName] = useState(
    location.ok ? location.fields.name : "",
  );
  // A value Nexara cannot read can only be removed, and only when asked to.
  const [removeWakeOnLan, setRemoveWakeOnLan] = useState(false);
  const [removeLocation, setRemoveLocation] = useState(false);

  // --- The checks ---
  const delayCheck = checkInteger(delay, 0, 300);
  const targetCheck = support.ballooningTarget
    ? checkInteger(target, 0, 100)
    : checkInteger("", 0, 100);

  const macCheck = checkMacAddress(mac);
  // No MAC is no Wake-on-LAN setting at all, and the interface and broadcast
  // address go with it: they are neither checked nor sent.
  const macEntered = mac.trim() !== "";
  const interfaceCheck =
    macEntered && support.wolBindBroadcast
      ? checkInterfaceName(bindInterface)
      : checkInterfaceName("");
  const broadcastCheck =
    macEntered && support.wolBindBroadcast
      ? checkIPv4Address(broadcast)
      : checkIPv4Address("");

  const latitudeCheck = checkCoordinate(latitude, "latitude", 90);
  const longitudeCheck = checkCoordinate(longitude, "longitude", 180);
  const nameCheck = checkLocationName(locationName);
  // pve-node-location requires both coordinates, and a name has no meaning
  // without them; with all three empty there is no location at all.
  const locationSet = [latitude, longitude, locationName].some(
    (text) => text.trim() !== "",
  );
  const latitudeError =
    errorOf(latitudeCheck) ??
    (locationSet && latitude.trim() === ""
      ? "A location needs both coordinates: enter a latitude."
      : null);
  const longitudeError =
    errorOf(longitudeCheck) ??
    (locationSet && longitude.trim() === ""
      ? "A location needs both coordinates: enter a longitude."
      : null);
  const nameError = errorOf(nameCheck);

  const problems = [
    errorOf(delayCheck),
    errorOf(targetCheck),
    errorOf(macCheck),
    errorOf(interfaceCheck),
    errorOf(broadcastCheck),
    support.location ? latitudeError : null,
    support.location ? longitudeError : null,
    support.location ? nameError : null,
  ].filter((error) => error !== null);

  // --- What would be sent ---
  // Every setting but the notes: those are the Notes dialog's, read from a route
  // of their own, and a key added here would not compile.
  const drafts: Omit<NodeOptionDrafts, "description"> = {
    "startall-onboot-delay": valueOf(delayCheck, null),
  };
  if (support.ballooningTarget) {
    drafts["ballooning-target"] = valueOf(targetCheck, null);
  }
  if (wakeOnLan.ok) {
    drafts.wakeonlan = macEntered
      ? buildWakeOnLan(
          {
            mac: valueOf(macCheck, ""),
            bindInterface: valueOf(interfaceCheck, ""),
            broadcastAddress: valueOf(broadcastCheck, ""),
          },
          opened.wakeonlan ?? "",
        )
      : null;
  } else if (removeWakeOnLan) {
    drafts.wakeonlan = null;
  }
  if (support.location) {
    if (location.ok) {
      drafts.location = locationSet
        ? buildLocation(
            {
              latitude: valueOf(latitudeCheck, ""),
              longitude: valueOf(longitudeCheck, ""),
              name: valueOf(nameCheck, ""),
            },
            opened.location ?? "",
          )
        : null;
    } else if (removeLocation) {
      drafts.location = null;
    }
  }
  const changes =
    problems.length === 0 ? changedNodeOptions(opened, drafts) : {};

  const { save, pending, error, conflict, latest } = useNodeOptionsSave({
    clusterId,
    nodeName,
    opened,
    subject: "options",
    reread,
    onSaved: onClose,
  });
  // What the re-read after a 409 found, against what this dialog opened with:
  // the card that shows it is behind the overlay. Only what this dialog shows.
  const changedSince =
    latest === null ? [] : optionsChangedBetween(opened, latest, support);

  // Held for a re-read in flight as well: it is about to move the digest a
  // save is made against. Not for the card's query being in any state, though:
  // a refresh that failed keeps the data, and the dialog has what it needs.
  const canSave =
    problems.length === 0 &&
    Object.keys(changes).length > 0 &&
    !pending &&
    conflict !== "rereading";

  const handleSubmit = (event: React.SyntheticEvent) => {
    event.preventDefault();
    // Save is disabled until there is something to send, but a submit that does
    // not go through the button (Enter in a field, requestSubmit) would still
    // send it.
    if (canSave) save(changes);
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="max-w-xl" fallbackFocus={fallbackFocus}>
        <DialogHeader>
          <DialogTitle>Edit Options - {nodeName}</DialogTitle>
          <DialogDescription>
            Empty settings use Proxmox's defaults.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} noValidate className="space-y-4">
          <TextField
            id={fieldId("delay")}
            label="Start on boot delay (seconds)"
            value={delay}
            onChange={setDelay}
            error={errorOf(delayCheck)}
            hint="Initial delay in seconds, before starting all the guests with on-boot enabled (0 to 300)."
            placeholder="0"
            inputMode="numeric"
          />
          {support.ballooningTarget && (
            <TextField
              id={fieldId("target")}
              label="RAM ballooning target (%)"
              value={target}
              onChange={setTarget}
              error={errorOf(targetCheck)}
              hint="RAM usage target for ballooning, in percent of the node's total memory (0 to 100)."
              placeholder="80"
              inputMode="numeric"
            />
          )}

          <fieldset className="space-y-3 rounded-md border p-3">
            <legend className="px-1 text-sm font-medium">Wake-on-LAN</legend>
            {wakeOnLan.ok ? (
              <>
                <TextField
                  id={fieldId("mac")}
                  label="MAC address"
                  value={mac}
                  onChange={setMac}
                  error={errorOf(macCheck)}
                  hint={
                    mac.trim() === "" && wakeOnLan.fields.mac !== ""
                      ? "Clearing the MAC address removes Wake-on-LAN, including the interface and broadcast address."
                      : "The MAC address this node is woken by."
                  }
                  placeholder="02:00:00:00:00:01"
                />
                {support.wolBindBroadcast && (
                  <>
                    <TextField
                      id={fieldId("bind-interface")}
                      label="Interface"
                      value={bindInterface}
                      onChange={setBindInterface}
                      error={errorOf(interfaceCheck)}
                      placeholder="Default route interface"
                      disabled={!macEntered}
                      describedBy={fieldId("sending")}
                    />
                    <TextField
                      id={fieldId("broadcast")}
                      label="Broadcast address"
                      value={broadcast}
                      onChange={setBroadcast}
                      error={errorOf(broadcastCheck)}
                      placeholder="255.255.255.255"
                      disabled={!macEntered}
                      describedBy={fieldId("sending")}
                    />
                    {/* Proxmox reads these two from the node that SENDS the
                        wake packet (the wakeonlan method, PVE/API2/Nodes.pm),
                        not from the one being woken. */}
                    <div
                      id={fieldId("sending")}
                      className="space-y-1 text-xs text-muted-foreground"
                    >
                      <p>
                        The interface and broadcast address are what this node
                        uses when it sends a wake packet to another node.
                      </p>
                      {!macEntered && (
                        <p>
                          Enter this node's MAC address first — Proxmox requires
                          it.
                        </p>
                      )}
                    </div>
                  </>
                )}
                {wakeOnLan.unknownSegments.length > 0 && (
                  <KeptAsIs
                    segments={wakeOnLan.unknownSegments}
                    removed={!macEntered}
                  />
                )}
              </>
            ) : (
              <Unreadable
                id={fieldId("remove-wakeonlan")}
                raw={opened.wakeonlan ?? ""}
                label="Remove this Wake-on-LAN setting"
                checked={removeWakeOnLan}
                onChange={setRemoveWakeOnLan}
              />
            )}
          </fieldset>

          {support.location && (
            <fieldset className="space-y-3 rounded-md border p-3">
              <legend className="px-1 text-sm font-medium">Location</legend>
              {location.ok ? (
                <>
                  <p
                    id={fieldId("location-help")}
                    className="text-xs text-muted-foreground"
                  >
                    Leave empty to use the datacenter's location.
                  </p>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <TextField
                      id={fieldId("latitude")}
                      label="Latitude"
                      value={latitude}
                      onChange={setLatitude}
                      error={latitudeError}
                      placeholder="-90 to 90"
                      describedBy={fieldId("location-help")}
                    />
                    <TextField
                      id={fieldId("longitude")}
                      label="Longitude"
                      value={longitude}
                      onChange={setLongitude}
                      error={longitudeError}
                      placeholder="-180 to 180"
                      describedBy={fieldId("location-help")}
                    />
                  </div>
                  <TextField
                    id={fieldId("location-name")}
                    label="Name"
                    value={locationName}
                    onChange={setLocationName}
                    error={nameError}
                    describedBy={fieldId("location-help")}
                  />
                  {location.unknownSegments.length > 0 && (
                    <KeptAsIs
                      segments={location.unknownSegments}
                      removed={!locationSet}
                    />
                  )}
                </>
              ) : (
                <Unreadable
                  id={fieldId("remove-location")}
                  raw={opened.location ?? ""}
                  label="Remove this location"
                  checked={removeLocation}
                  onChange={setRemoveLocation}
                />
              )}
            </fieldset>
          )}

          {error !== "" && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
          <ConflictNote
            conflict={conflict}
            repinned={<OptionsRepinned changed={changedSince} />}
          />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={!canSave}>
              {pending ? "Saving..." : "Save"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/**
 * A text field whose check failed says why under itself, tied to the input
 * (aria-invalid, aria-describedby) and with none of the browser's own limits
 * (min, max, maxLength, pattern): those would stop the typing or the submit
 * with a message of the browser's wording, where the check is Proxmox's own.
 */
function TextField({
  id,
  label,
  value,
  onChange,
  error,
  hint,
  placeholder,
  inputMode,
  disabled,
  describedBy: sharedHelp,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  error: string | null;
  hint?: string;
  placeholder?: string;
  inputMode?: "numeric";
  disabled?: boolean;
  /** The id of text shared with other fields, which describes this one too. */
  describedBy?: string;
}) {
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const describedBy =
    [
      hint === undefined ? null : hintId,
      sharedHelp ?? null,
      error === null ? null : errorId,
    ]
      .filter((part) => part !== null)
      .join(" ") || undefined;
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type="text"
        inputMode={inputMode}
        value={value}
        onChange={(event) => {
          onChange(event.target.value);
        }}
        placeholder={placeholder}
        disabled={disabled}
        autoComplete="off"
        spellCheck={false}
        aria-invalid={error === null ? undefined : true}
        aria-describedby={describedBy}
      />
      {hint !== undefined && (
        <p id={hintId} className="text-xs text-muted-foreground">
          {hint}
        </p>
      )}
      {error !== null && (
        <p id={errorId} className="text-xs text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}

/**
 * A stored value Nexara cannot read — Proxmox's own read drops one that fails
 * its format, so this is defensive — shown as it is stored and offered only for
 * removal. Left unticked it is not sent at all.
 */
function Unreadable({
  id,
  raw,
  label,
  checked,
  onChange,
}: {
  id: string;
  raw: string;
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <div className="space-y-2">
      <p className="break-all rounded-md bg-muted px-2 py-1.5 font-mono text-xs">
        {raw}
      </p>
      <div className="flex items-center gap-2">
        <Checkbox
          id={id}
          checked={checked}
          onCheckedChange={(state) => {
            onChange(state === true);
          }}
        />
        <Label htmlFor={id}>{label}</Label>
      </div>
      <p className="text-xs text-muted-foreground">
        Nexara cannot read this value, so it can only be removed here; change it
        in the Proxmox UI.
      </p>
    </div>
  );
}

/**
 * Settings stored with a value that this form has no field for. They are kept
 * as they are while the setting is saved, and go with it when the whole key is
 * removed (`removed`): the form must not say they stay when they do not.
 */
function KeptAsIs({
  segments,
  removed,
}: {
  segments: string[];
  removed: boolean;
}) {
  return (
    <p className="break-all text-xs text-muted-foreground">
      {removed
        ? "Also stored with this setting and removed along with it: "
        : "Also stored with this setting and kept as is: "}
      <span className="font-mono">{segments.join(", ")}</span>
    </p>
  );
}

/**
 * The note after a 409 whose re-read succeeded: which of the settings this
 * dialog shows differ now from what it opened with, since the card that shows
 * the new values is behind the overlay, and what saving again does.
 */
function OptionsRepinned({ changed }: { changed: NodeOptionReadKey[] }) {
  return (
    <>
      <p>
        {changed.length > 0
          ? `Changed since you opened this: ${changed
              .map((key) => NODE_OPTION_LABELS[key])
              .join(", ")}.`
          : "None of the settings shown here changed; something else in the node's configuration did."}
      </p>
      <p>
        Saving again writes the settings you changed here over the node's
        current values for them; the rest are left as they are.
      </p>
    </>
  );
}
