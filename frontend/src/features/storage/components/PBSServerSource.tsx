import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  usePBSDatastores,
  usePBSServers,
} from "@/features/backup/api/backup-queries";
import type { PBSServer } from "@/features/backup/types/backup";
import {
  pbsAddress,
  pbsAddressLabel,
  type PBSCredentials,
  type PBSSource,
} from "../lib/pbs-source";

// The select's value for "no registered server": Radix keeps "" for no value.
const MANUAL = "_manual";

interface PBSServerSourceProps {
  /** The cluster the storage is for. */
  clusterId: string;
  value: PBSSource | null;
  onChange: (next: PBSSource | null) => void;
  /** The caller holds manage:pbs, which handing a server's token to Proxmox takes. */
  canManagePBS: boolean;
}

/**
 * Picks one of the PBS servers registered under Backup for a pbs storage, and
 * the token it signs in with. Renders nothing when the caller can see no
 * registered server, which leaves every detail to be typed, as before.
 */
export function PBSServerSource({
  clusterId,
  value,
  onChange,
  canManagePBS,
}: PBSServerSourceProps) {
  const serversQuery = usePBSServers();
  const servers = serversQuery.data ?? [];
  if (servers.length === 0) return null;

  // The server side hands a server's token only to the cluster it is attached
  // to (pbsSourceSettings), since the token travels through that cluster's API
  // URL.
  const tokenUsable = (server: PBSServer) =>
    canManagePBS && server.cluster_id === clusterId;

  const address = value === null ? null : pbsAddress(value.server.api_url);
  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <Label htmlFor="pbs-source">PBS server</Label>
        <Select
          value={value?.server.id ?? MANUAL}
          onValueChange={(id) => {
            const server = servers.find((s) => s.id === id);
            onChange(
              server === undefined
                ? null
                : {
                    server,
                    credentials: tokenUsable(server)
                      ? "server-token"
                      : "own-token",
                  },
            );
          }}
        >
          <SelectTrigger id="pbs-source">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={MANUAL}>Enter the details below</SelectItem>
            {servers.map((s) => (
              <SelectItem key={s.id} value={s.id}>
                {s.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {value !== null && (
          <p className="text-xs text-muted-foreground">
            Proxmox connects to{" "}
            {address === null ? value.server.api_url : pbsAddressLabel(address)}
            {value.server.tls_fingerprint !== "" &&
              ", trusting the certificate Nexara pinned for it"}
            .
          </p>
        )}
      </div>

      {value !== null && (
        <div className="space-y-1.5">
          <Label htmlFor="pbs-credentials">Token</Label>
          <Select
            value={value.credentials}
            onValueChange={(credentials) => {
              onChange({
                ...value,
                credentials: credentials as PBSCredentials,
              });
            }}
          >
            <SelectTrigger id="pbs-credentials">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {tokenUsable(value.server) && (
                <SelectItem value="server-token">
                  This server&apos;s token ({value.server.token_id})
                </SelectItem>
              )}
              <SelectItem value="own-token">Another token</SelectItem>
            </SelectContent>
          </Select>
          {canManagePBS && value.server.cluster_id !== clusterId && (
            <p className="text-xs text-muted-foreground">
              This server&apos;s own token can be used only for storage on the
              cluster it is attached to.
            </p>
          )}
          {value.credentials === "server-token" && (
            <p className="text-xs text-amber-700 dark:text-amber-400">
              Proxmox keeps its own copy of this token and backs up with every
              right it holds on the PBS: a token with only the DatastoreBackup
              role on this datastore is safer. Replacing the token in Nexara
              later won&apos;t update this storage.
            </p>
          )}
        </div>
      )}
    </div>
  );
}

interface PBSDatastoreFieldProps {
  id: string;
  serverId: string;
  value: string;
  onChange: (name: string) => void;
}

/**
 * A registered server's datastore, picked from the ones Nexara can list, or
 * typed when it cannot list them (no view:backup, or the PBS did not answer).
 */
export function PBSDatastoreField({
  id,
  serverId,
  value,
  onChange,
}: PBSDatastoreFieldProps) {
  const datastoresQuery = usePBSDatastores(serverId);
  const names = (datastoresQuery.data ?? []).map((d) => d.store);

  if (names.length === 0) {
    return (
      <>
        <Input
          id={id}
          value={value}
          onChange={(e) => {
            onChange(e.target.value);
          }}
          placeholder={
            datastoresQuery.isLoading ? "Loading datastores..." : "main"
          }
        />
        {datastoresQuery.isError && (
          <p className="text-xs text-muted-foreground">
            Nexara could not list this server&apos;s datastores, so type the
            name.
          </p>
        )}
      </>
    );
  }
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger id={id}>
        <SelectValue placeholder="Select a datastore" />
      </SelectTrigger>
      <SelectContent>
        {names.map((name) => (
          <SelectItem key={name} value={name}>
            {name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
