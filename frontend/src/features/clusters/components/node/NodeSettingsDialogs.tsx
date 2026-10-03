import {
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  CancelledError,
  useIsMutating,
  type QueryObserverResult,
} from "@tanstack/react-query";
import { Loader2, Pencil } from "lucide-react";
import { toast } from "sonner";

import { QueryFailureNote } from "@/components/QueryStateNotice";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { sessionScope } from "@/lib/api-client";
import { describeError } from "@/lib/api-error";
import {
  nodeSettingSaveKey,
  useNodeDNS,
  useNodeTime,
  useSetNodeDNS,
  useSetNodeTimezone,
  type NodeDNSResponse,
  type NodeTimeResponse,
} from "../../api/cluster-queries";

/**
 * The Edit button of the node page's Network & DNS card, and the dialog it
 * opens. The page draws it only for a user who can manage the node, on an
 * online one, so for anyone else there is nothing here to show and nothing to
 * read.
 *
 * The write replaces all four settings (PUT …/dns, internal/api/
 * registry_nodes.go: a resolver sent empty is removed), so the form must start
 * from what the node has, or it would clear what the operator never touched.
 * EditFromRead is how it gets there.
 */
export function EditDNSDialog({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const query = useNodeDNS(clusterId, nodeName);
  const saving =
    useIsMutating({
      mutationKey: nodeSettingSaveKey(clusterId, nodeName, "dns"),
    }) > 0;
  return (
    <EditFromRead
      label="Edit DNS settings"
      subject={`the DNS settings of ${nodeName}`}
      query={query}
      saving={saving}
      dialog={({ opened, close, fallbackFocus }) => (
        <DNSDialog
          clusterId={clusterId}
          nodeName={nodeName}
          opened={opened}
          onClose={close}
          fallbackFocus={fallbackFocus}
        />
      )}
    />
  );
}

/**
 * The Edit button of the node page's System card for the timezone, and the
 * dialog it opens: the same rules as EditDNSDialog.
 *
 * The form starts from the timezone the node itself reports (useNodeTime), not
 * from the node list's copy of it, which the collector stores: that is empty
 * when its last read of the node failed, and behind a change until its next
 * sync. A timezone that is not known is left empty, not filled in with a
 * default: the form would offer to set a node to a timezone nobody chose, one
 * click away.
 */
export function EditTimezoneDialog({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const query = useNodeTime(clusterId, nodeName);
  const saving =
    useIsMutating({
      mutationKey: nodeSettingSaveKey(clusterId, nodeName, "time"),
    }) > 0;
  return (
    <EditFromRead
      label="Edit timezone"
      subject={`the timezone of ${nodeName}`}
      query={query}
      saving={saving}
      dialog={({ opened, close, fallbackFocus }) => (
        <TimezoneDialog
          clusterId={clusterId}
          nodeName={nodeName}
          opened={opened}
          onClose={close}
          fallbackFocus={fallbackFocus}
        />
      )}
    />
  );
}

/**
 * What the Network & DNS card says when its read failed and left nothing to
 * edit from, under its rows, where the Options card says the same of its own:
 * the Edit button is not drawn without that read, and a button that is simply
 * absent explains nothing. Not drawn once there is data: a read that fails
 * after that is for EditFromRead to say, when Edit is pressed.
 */
export function DNSReadNote({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const query = useNodeDNS(clusterId, nodeName);
  if (query.data !== undefined) return null;
  return (
    <QueryFailureNote
      query={query}
      subject="this node's DNS settings"
      identity={nodeName}
      className="mt-2"
    />
  );
}

/**
 * What the System card says of a timezone it could not read: see DNSReadNote.
 */
export function TimezoneReadNote({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const query = useNodeTime(clusterId, nodeName);
  if (query.data !== undefined) return null;
  return (
    <QueryFailureNote
      query={query}
      subject="this node's timezone"
      identity={nodeName}
      className="mt-2"
    />
  );
}

/**
 * What EditFromRead needs of a query: the page's read, and a way to read again.
 */
interface Readable<T> {
  data: T | undefined;
  refetch: () => Promise<QueryObserverResult<T>>;
}

/**
 * An Edit button, and the dialog it opens from a read made when it was pressed.
 *
 * The button is drawn once the page's own read is in hand (its data, which a
 * refresh that failed keeps), but the dialog is not drawn from that read. It is
 * what the node held when it was made, and nothing says the node still does: a
 * change made elsewhere, or by the save that has just closed a dialog, leaves
 * it behind for as long as the cache calls it fresh, and a refresh that failed
 * leaves it there indefinitely. These settings carry no digest to catch that,
 * and the write replaces every one of them, so a form drawn from an old read
 * puts the old values back. Pressing Edit therefore reads the node again, and
 * the dialog opens only from a read that SUCCEEDED and was MADE AFTER THE
 * PRESS, seeded once from that result and from nothing a refresh does
 * afterwards. Success alone is not that. After a failed read the data is still
 * there, and is the old one; and a read that is cancelled with nothing to
 * replace it resolves as a success carrying the old data too: cancelQueries
 * puts the query back as it was, and clear() or removeQueries() drop it from
 * under the read. That is a sign-out as often as anything, and it is not a
 * failure, so it opens nothing and says nothing; so does a cancellation that
 * leaves the query in error. The data must therefore also be newer than the
 * press (dataUpdatedAt), which is what keeps all of them out. A count of the
 * query's data updates would say the same without the clock, but resetQueries()
 * zeroes it and then refetches, which is a fresh read that a count would
 * refuse. Both stamps are the browser's clock. One set back during a read makes
 * a real read look old, and the press then opens nothing, so pressing again is
 * the answer. One set back between the page's read and the press does the
 * opposite: the old data's stamp is then not older than the press, and a read
 * that is cancelled, cleared or removed (or a write to the cache by other code,
 * then reverted) would be taken for fresh. Nothing does that to these queries
 * today: only a sign-out cancels or clears them, and it unmounts this.
 *
 * The button is held while the read is out, disabled and busy, so that a second
 * press cannot start another; and while a save of the setting is pending
 * (`saving`): a dialog dismissed with its save in flight leaves a write that
 * has not landed, and a read made before it does would be of the old setting.
 * Its title says which, since a disabled button otherwise does not say why.
 *
 * A read that fails opens nothing and says so in a toast naming the node, which
 * a button that merely did nothing would not; the button stays, so pressing it
 * again is the retry. A read that ends after this is unmounted (the page was
 * left, the node changed, the permission was lost) opens nothing and says
 * nothing: `live`. So does one that ends after the session it was pressed in
 * has (sessionScope): a sign-out, an expiry or another user signing in. A toast
 * raised after any of the three is shown to whoever is signed in by then, and
 * this one names the node.
 *
 * `fallbackFocus` is the button itself, which a dialog sends focus back to when
 * the button had none to give back: a button that is disabled while it reads
 * can lose it, in browsers that move focus off an element that becomes
 * disabled. The same goes for a press that ends with nothing opened, where
 * nothing else would return it: once the button is enabled again it takes focus
 * back, if nothing else has it.
 */
function EditFromRead<T>({
  label,
  subject,
  query,
  saving,
  dialog,
}: {
  /** The button's accessible name. */
  label: string;
  /**
   * What a read that failed says it could not load: "the timezone of pve-01".
   */
  subject: string;
  query: Readable<T>;
  saving: boolean;
  dialog: (open: {
    opened: T;
    close: () => void;
    fallbackFocus: () => HTMLElement | null;
  }) => ReactNode;
}) {
  const [opened, setOpened] = useState<T | null>(null);
  const [reading, setReading] = useState(false);
  const button = useRef<HTMLButtonElement>(null);

  // Whether this is still mounted. A layout effect, so that it clears in the
  // commit that removes it and not in the passive flush after it, as in
  // useNodeOptionsSave: a read settling in between would find it live.
  const live = useRef(false);
  useLayoutEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, []);

  // Set when a press ended with nothing opened. Focus is given back in a layout
  // effect, once `reading` is false: the button is enabled again by then, and a
  // disabled one cannot take focus. Without scrolling: a press that ends slowly
  // may find the user scrolled away, and focus() would take the page back up to
  // the card.
  const refocus = useRef(false);
  useLayoutEffect(() => {
    if (reading || !refocus.current) return;
    refocus.current = false;
    const active = document.activeElement;
    if (active === null || active === document.body) {
      button.current?.focus({ preventScroll: true });
    }
  }, [reading]);

  const read = () => {
    // The hold is the button's `disabled` attribute, which browsers and React
    // both honour, so a click on a held button never gets here. This is the
    // same hold again, for the day the button is held with aria-disabled
    // instead, which takes clicks. Through a disabled button nothing in the DOM
    // gets past the attribute to reach it: NodeSettingsDialogs.held.test.tsx
    // uses a Button that is held the other way.
    if (reading || saving) return;
    const pressedAt = Date.now();
    // The session this was pressed in, checked ahead of `live` below: a read
    // that ends once its session is over is for nobody who is here.
    const ended = sessionScope();
    setReading(true);
    // refetch() cancels a fetch already in flight and starts its own, so an
    // older read still on its way cannot answer for the press. It resolves,
    // whatever happened, with the query's result, and a success is not proof of
    // a read: see the comment above, and the dataUpdatedAt check.
    void query.refetch().then((result) => {
      if (ended()) {
        // It opens nothing and says nothing: what it found, or failed with, is
        // not for whoever is signed in now. What holds the button is let go all
        // the same, as the other sites let go of their guards whatever the
        // session: a button that is somehow still there must not stay reading.
        if (live.current) setReading(false);
        return;
      }
      if (!live.current) return;
      if (result.isSuccess && result.dataUpdatedAt >= pressedAt) {
        setReading(false);
        setOpened(result.data);
        return;
      }
      refocus.current = true;
      setReading(false);
      if (result.isError && !(result.error instanceof CancelledError)) {
        const reason = describeError(result.error);
        toast.error(
          `Could not load ${subject}${reason !== "" ? `: ${reason}` : "."}`,
        );
      }
    });
  };

  // Why the button is held, which a disabled button does not say of itself. Its
  // title shows on hover, since it is pointer-events-auto when disabled too.
  const held = reading
    ? `Reading ${subject}...`
    : saving
      ? `Saving ${subject}...`
      : undefined;

  return (
    <>
      {query.data !== undefined && (
        <Button
          ref={button}
          aria-label={label}
          aria-busy={reading || undefined}
          title={held}
          variant="ghost"
          size="icon"
          className="h-6 w-6 disabled:pointer-events-auto"
          disabled={reading || saving}
          onClick={read}
        >
          {reading ? (
            <Loader2 className="h-3 w-3 animate-spin" />
          ) : (
            <Pencil className="h-3 w-3" />
          )}
        </Button>
      )}
      {opened !== null &&
        dialog({
          opened,
          close: () => {
            setOpened(null);
          },
          fallbackFocus: () => button.current,
        })}
    </>
  );
}

/**
 * Edits one node's DNS settings. Mounted by EditFromRead for ONE open and
 * seeded once from `opened`, so it has no state in which the fields are empty
 * for want of a read, and nothing that happens to the read afterwards reaches
 * it. All four settings are sent, as they stand in the form: the API replaces
 * every one, so the ones left alone are sent as they were read.
 */
function DNSDialog({
  clusterId,
  nodeName,
  opened,
  onClose,
  fallbackFocus,
}: {
  clusterId: string;
  nodeName: string;
  opened: NodeDNSResponse;
  onClose: () => void;
  fallbackFocus: () => HTMLElement | null;
}) {
  const uid = useId();
  const [search, setSearch] = useState(opened.search);
  const [dns1, setDns1] = useState(opened.dns1);
  const [dns2, setDns2] = useState(opened.dns2);
  const [dns3, setDns3] = useState(opened.dns3);
  const setNodeDNS = useSetNodeDNS(clusterId, nodeName);

  const handleSave = () => {
    setNodeDNS.mutate(
      { search, dns1, dns2, dns3 },
      {
        onSuccess: () => {
          onClose();
        },
      },
    );
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent fallbackFocus={fallbackFocus}>
        <DialogHeader>
          <DialogTitle>Edit DNS Configuration - {nodeName}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor={`${uid}-search`}>Search Domain</Label>
            <Input
              id={`${uid}-search`}
              value={search}
              onChange={(e) => {
                setSearch(e.target.value);
              }}
              placeholder="e.g. example.com"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${uid}-dns1`}>DNS Server 1</Label>
            <Input
              id={`${uid}-dns1`}
              value={dns1}
              onChange={(e) => {
                setDns1(e.target.value);
              }}
              placeholder="e.g. 8.8.8.8"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${uid}-dns2`}>DNS Server 2</Label>
            <Input
              id={`${uid}-dns2`}
              value={dns2}
              onChange={(e) => {
                setDns2(e.target.value);
              }}
              placeholder="e.g. 8.8.4.4"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${uid}-dns3`}>DNS Server 3</Label>
            <Input
              id={`${uid}-dns3`}
              value={dns3}
              onChange={(e) => {
                setDns3(e.target.value);
              }}
              placeholder="Optional"
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button
              onClick={handleSave}
              disabled={!search || setNodeDNS.isPending}
            >
              Save
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

/**
 * Edits one node's timezone. Mounted by EditFromRead for ONE open and seeded
 * once from `opened`, as DNSDialog is. An empty timezone cannot be saved.
 */
function TimezoneDialog({
  clusterId,
  nodeName,
  opened,
  onClose,
  fallbackFocus,
}: {
  clusterId: string;
  nodeName: string;
  opened: NodeTimeResponse;
  onClose: () => void;
  fallbackFocus: () => HTMLElement | null;
}) {
  const uid = useId();
  const [timezone, setTimezone] = useState(opened.timezone);
  const setNodeTimezone = useSetNodeTimezone(clusterId, nodeName);

  const handleSave = () => {
    setNodeTimezone.mutate(
      { timezone },
      {
        onSuccess: () => {
          onClose();
        },
      },
    );
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent fallbackFocus={fallbackFocus}>
        <DialogHeader>
          <DialogTitle>Edit Timezone - {nodeName}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor={`${uid}-timezone`}>Timezone</Label>
            <Input
              id={`${uid}-timezone`}
              value={timezone}
              onChange={(e) => {
                setTimezone(e.target.value);
              }}
              placeholder="e.g. Europe/London"
            />
            <p className="text-xs text-muted-foreground">
              Enter an IANA timezone (e.g. UTC, Europe/London)
            </p>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button
              onClick={handleSave}
              disabled={!timezone || setNodeTimezone.isPending}
            >
              Save
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
