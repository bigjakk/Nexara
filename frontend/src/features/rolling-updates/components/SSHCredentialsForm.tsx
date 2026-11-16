import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { SettingRow, SettingsFooter } from "@/components/page/Settings";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Segmented } from "@/components/ui/segmented";
import { Loader2, Trash2 } from "lucide-react";
import {
  useSSHCredentials,
  useUpsertSSHCredentials,
  useDeleteSSHCredentials,
  useTestSSHConnection,
  useSSHKnownHosts,
  usePinSSHHostKey,
  useDeleteSSHKnownHost,
} from "../api/rolling-update-queries";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import type {
  SSHCredential,
  SSHHostKeyPending,
  SSHHostKeyMismatch,
  SSHKnownHost,
} from "@/types/api";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import { BulkPinDialog } from "./BulkPinDialog";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { describeError } from "@/lib/api-error";

interface SSHCredentialsFormProps {
  clusterId: string;
}

type TestUiState =
  | { kind: "idle" }
  | { kind: "success"; fingerprint?: string; notRoot: boolean }
  | { kind: "error"; message: string }
  | {
      kind: "pending";
      info: SSHHostKeyPending;
      nodeName: string;
    }
  | {
      kind: "mismatch";
      info: SSHHostKeyMismatch;
      nodeName: string;
    };

export function SSHCredentialsForm({ clusterId }: SSHCredentialsFormProps) {
  const { t } = useTranslation("security");
  const { data: creds, isLoading } = useSSHCredentials(clusterId);
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: knownHosts } = useSSHKnownHosts(clusterId);
  const upsert = useUpsertSSHCredentials();
  const deleteCreds = useDeleteSSHCredentials();
  const testConn = useTestSSHConnection();
  const pinKey = usePinSSHHostKey();
  // The form shows a failed test or pin itself (the hooks opt out of the app's
  // toast), and one that lands after it was left is toasted instead.
  const settle = useSaveOutcome(clusterId);
  const deleteKnownHost = useDeleteSSHKnownHost();

  const [editing, setEditing] = useState(false);
  const [username, setUsername] = useState("root");
  const [port, setPort] = useState(22);
  const [authType, setAuthType] = useState<"password" | "key">("password");
  const [password, setPassword] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [testNode, setTestNode] = useState("");
  const [testState, setTestState] = useState<TestUiState>({ kind: "idle" });
  const [bulkOpen, setBulkOpen] = useState(false);
  const [pendingCredsDelete, setPendingCredsDelete] =
    useState<SSHCredential | null>(null);
  const [pendingUnpin, setPendingUnpin] = useState<SSHKnownHost | null>(null);

  // Compute pinned-vs-unpinned status by matching each cluster node's
  // address against the pinned-hosts list. Nodes without a stored address
  // (collector hasn't reported yet) are counted as unpinned.
  const pinSummary = useMemo(() => {
    const pinnedAddresses = new Set((knownHosts ?? []).map((kh) => kh.host));
    const total = nodes?.length ?? 0;
    let pinned = 0;
    for (const n of nodes ?? []) {
      if (n.address && pinnedAddresses.has(n.address)) pinned += 1;
    }
    return { pinned, total };
  }, [nodes, knownHosts]);

  const unpinnedNodes = useMemo(() => {
    const pinnedAddresses = new Set((knownHosts ?? []).map((kh) => kh.host));
    return (nodes ?? []).filter(
      (n) => !n.address || !pinnedAddresses.has(n.address),
    );
  }, [nodes, knownHosts]);

  const startEditing = () => {
    if (creds) {
      setUsername(creds.username);
      setPort(creds.port);
      setAuthType(creds.auth_type);
    }
    setPassword("");
    setPrivateKey("");
    setTestState({ kind: "idle" });
    setEditing(true);
  };

  const handleSave = () => {
    const body: Parameters<typeof upsert.mutate>[0] = {
      clusterId,
      username,
      port,
      auth_type: authType,
    };
    if (authType === "password") {
      body.password = password;
    } else {
      body.private_key = privateKey;
    }
    upsert.mutate(body, {
      onSuccess: () => {
        setEditing(false);
        setPassword("");
        setPrivateKey("");
        // Rolling updates require every node's host key to be pinned. Launch
        // the bulk-pin flow automatically so it isn't a separate step the
        // user has to remember after saving credentials.
        if (unpinnedNodes.length > 0) {
          setBulkOpen(true);
        }
      },
    });
  };

  const handleTest = () => {
    setTestState({ kind: "idle" });
    const firstNode = nodes?.[0];
    const nodeName = testNode || (firstNode ? firstNode.name : "");
    if (!nodeName) return;
    settle(testConn.mutateAsync({ clusterId, nodeName }), {
      action: t("sshTestAction", { node: nodeName }),
      onSuccess: (result) => {
        if (result.success) {
          // A warning, never a refusal: only an explicit false warns.
          const notRoot = result.root === false;
          setTestState(
            result.fingerprint !== undefined
              ? { kind: "success", fingerprint: result.fingerprint, notRoot }
              : { kind: "success", notRoot },
          );
          return;
        }
        if (result.host_key_pending) {
          setTestState({
            kind: "pending",
            info: result.host_key_pending,
            nodeName,
          });
          return;
        }
        if (result.host_key_mismatch) {
          setTestState({
            kind: "mismatch",
            info: result.host_key_mismatch,
            nodeName,
          });
          return;
        }
        setTestState({ kind: "error", message: result.message });
      },
      onError: (err) => {
        setTestState({
          kind: "error",
          message: describeError(err) || "Request failed",
        });
      },
    });
  };

  const handlePin = (nodeName: string, expectedFingerprint: string) => {
    settle(pinKey.mutateAsync({ clusterId, nodeName, expectedFingerprint }), {
      action: t("sshPinAction", { node: nodeName }),
      onSuccess: () => {
        // Re-run the test against the freshly-pinned key.
        handleTest();
      },
      onError: (err) => {
        setTestState({
          kind: "error",
          message: err instanceof Error ? err.message : "Pin failed",
        });
      },
    });
  };

  const handleDelete = () => {
    deleteCreds.mutate(
      { clusterId },
      {
        onSuccess: () => {
          setEditing(false);
        },
      },
    );
  };

  if (isLoading) {
    return (
      <div className="flex justify-center py-4">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!editing && !creds) {
    return (
      <SettingRow
        label="No SSH credentials configured"
        description={
          <>
            Add credentials to enable automated{" "}
            <code className="text-xs">apt dist-upgrade</code> during rolling
            updates.
          </>
        }
      >
        <Button variant="outline" size="sm" onClick={startEditing}>
          Configure SSH
        </Button>
      </SettingRow>
    );
  }

  if (!editing && creds) {
    const allPinned = pinSummary.pinned === pinSummary.total;
    return (
      <>
        <SettingRow
          label="Credentials"
          description={
            <>
              Signs in as{" "}
              <span className="font-medium text-foreground">
                {creds.username}
              </span>{" "}
              on port{" "}
              <span className="font-medium text-foreground">{creds.port}</span>{" "}
              with {creds.auth_type === "key" ? "an SSH key" : "a password"}.
            </>
          }
        >
          <Button variant="outline" size="sm" onClick={startEditing}>
            Edit
          </Button>
          <Button
            aria-label="Delete SSH credentials"
            variant="ghost"
            size="icon"
            className="h-8 w-8 text-muted-foreground hover:text-destructive"
            onClick={() => {
              setPendingCredsDelete(creds);
            }}
            disabled={deleteCreds.isPending}
          >
            <Trash2 className="h-4 w-4" />
          </Button>
        </SettingRow>

        {pinSummary.total > 0 && (
          <SettingRow
            label="Host keys"
            description={
              allPinned
                ? "Every node's host key is pinned."
                : "Rolling updates require every node's host key to be pinned."
            }
          >
            <span
              className={
                allPinned
                  ? "flex items-center gap-[7px] text-sm text-muted-foreground"
                  : "flex items-center gap-[7px] text-sm text-amber-700 dark:text-amber-400"
              }
            >
              <span
                aria-hidden="true"
                className={
                  allPinned
                    ? "h-2 w-2 shrink-0 rounded-full bg-emerald-500"
                    : "h-2 w-2 shrink-0 rounded-full bg-amber-500"
                }
              />
              {pinSummary.pinned}/{pinSummary.total} pinned
            </span>
            {unpinnedNodes.length > 0 && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setBulkOpen(true);
                }}
              >
                Pin all node host keys
              </Button>
            )}
          </SettingRow>
        )}

        <SettingRow
          label="Test the connection"
          htmlFor="ssh-test-node"
          description="Signs in to one node with the stored credentials and checks its host key."
        >
          <select
            id="ssh-test-node"
            className="h-8 min-w-0 flex-1 rounded-md border border-input bg-transparent px-2 text-[13px] focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring sm:max-w-[13rem]"
            value={testNode}
            onChange={(e) => {
              setTestNode(e.target.value);
            }}
          >
            <option value="">Select node to test...</option>
            {nodes?.map((n) => (
              <option key={n.id} value={n.name}>
                {n.name}
              </option>
            ))}
          </select>
          <Button
            variant="outline"
            size="sm"
            onClick={handleTest}
            disabled={
              testConn.isPending ||
              (!testNode && (!nodes || nodes.length === 0))
            }
          >
            {testConn.isPending ? (
              <Loader2 className="mr-1 h-3 w-3 animate-spin" />
            ) : null}
            Test connection
          </Button>

          {testState.kind === "success" && (
            <p className="flex basis-full items-center gap-[7px] text-[12.5px] text-muted-foreground sm:justify-end">
              <span
                aria-hidden="true"
                className="h-2 w-2 shrink-0 rounded-full bg-emerald-500"
              />
              <span className="min-w-0 [overflow-wrap:anywhere]">
                SSH connection successful.
                {testState.fingerprint && (
                  <span className="ml-1 font-mono text-[11px]">
                    ({testState.fingerprint})
                  </span>
                )}
              </span>
            </p>
          )}

          {testState.kind === "success" && testState.notRoot && (
            <p className="flex basis-full items-baseline gap-[7px] text-[12.5px] text-amber-700 sm:justify-end dark:text-amber-400">
              <span
                aria-hidden="true"
                className="h-2 w-2 shrink-0 -translate-y-px rounded-full bg-amber-500"
              />
              <span className="min-w-0">{t("sshTestNotRoot")}</span>
            </p>
          )}

          {testState.kind === "error" && (
            <p className="basis-full text-[12.5px] text-red-700 [overflow-wrap:anywhere] sm:text-right dark:text-red-400">
              {testState.message}
            </p>
          )}
        </SettingRow>

        <BulkPinDialog
          open={bulkOpen}
          onOpenChange={setBulkOpen}
          clusterId={clusterId}
          nodes={(nodes ?? []).map((n) => ({
            name: n.name,
            address: n.address,
          }))}
        />

        {testState.kind === "pending" && (
          <div className="space-y-2.5 py-3.5 text-[13px]">
            <div className="flex items-baseline gap-2.5">
              <span
                aria-hidden="true"
                className="h-2 w-2 shrink-0 -translate-y-px rounded-full bg-amber-500"
              />
              <div>
                <p className="font-medium text-amber-700 dark:text-amber-400">
                  Host key not yet trusted.
                </p>
                <p className="mt-0.5 text-muted-foreground">
                  Compare this fingerprint to the value reported by the node
                  itself (run{" "}
                  <code className="text-xs">
                    ssh-keyscan {testState.info.host}
                  </code>{" "}
                  on a trusted machine, or check the node console).
                </p>
              </div>
            </div>
            <div className="ml-[18px] space-y-1 font-mono text-xs">
              <div>
                <span className="text-muted-foreground">host: </span>
                {testState.info.host}:{testState.info.port}
              </div>
              <div className="break-all">
                <span className="text-muted-foreground">fingerprint: </span>
                {testState.info.fingerprint}
              </div>
            </div>
            <div className="ml-[18px] flex gap-2">
              <Button
                size="sm"
                variant="outline"
                disabled={pinKey.isPending}
                onClick={() => {
                  handlePin(testState.nodeName, testState.info.fingerprint);
                }}
              >
                {pinKey.isPending && (
                  <Loader2 className="mr-1 h-3 w-3 animate-spin" />
                )}
                Trust &amp; Pin
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  setTestState({ kind: "idle" });
                }}
              >
                Cancel
              </Button>
            </div>
          </div>
        )}

        {testState.kind === "mismatch" && (
          <div className="space-y-2.5 py-3.5 text-[13px]">
            <div className="flex items-baseline gap-2.5">
              <span
                aria-hidden="true"
                className="h-2 w-2 shrink-0 -translate-y-px rounded-full bg-red-500"
              />
              <div>
                <p className="font-medium text-red-700 dark:text-red-400">
                  Host key has changed since it was pinned.
                </p>
                <p className="mt-0.5 text-muted-foreground">
                  This may be a legitimate reinstall or it may indicate a
                  man-in-the-middle. Investigate before re-pinning.
                </p>
              </div>
            </div>
            <div className="ml-[18px] space-y-1 font-mono text-xs">
              <div>
                <span className="text-muted-foreground">host: </span>
                {testState.info.host}:{testState.info.port}
              </div>
              <div className="break-all">
                <span className="text-muted-foreground">expected: </span>
                {testState.info.expected_fingerprint}
              </div>
              <div className="break-all">
                <span className="text-muted-foreground">presented: </span>
                {testState.info.presented_fingerprint}
              </div>
            </div>
            <div className="ml-[18px] flex gap-2">
              <Button
                size="sm"
                variant="destructive"
                disabled={pinKey.isPending}
                onClick={() => {
                  handlePin(
                    testState.nodeName,
                    testState.info.presented_fingerprint,
                  );
                }}
              >
                {pinKey.isPending && (
                  <Loader2 className="mr-1 h-3 w-3 animate-spin" />
                )}
                Re-pin to new key
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  setTestState({ kind: "idle" });
                }}
              >
                Cancel
              </Button>
            </div>
          </div>
        )}

        {knownHosts && knownHosts.length > 0 && (
          <div className="py-3.5 last:pb-0">
            <h3 className="text-sm font-medium">Pinned host keys</h3>
            <div className="mt-1 divide-y">
              {knownHosts.map((kh) => (
                <div
                  key={kh.id}
                  className="flex items-center justify-between gap-2 py-1.5 last:pb-0"
                >
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-mono text-[12.5px]">
                      {kh.host}:{kh.port}
                    </div>
                    <div className="truncate font-mono text-[11px] text-muted-foreground">
                      {kh.fingerprint}
                    </div>
                  </div>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-8 w-8 shrink-0 text-muted-foreground hover:text-destructive"
                    disabled={deleteKnownHost.isPending}
                    onClick={() => {
                      setPendingUnpin(kh);
                    }}
                    title="Unpin host key"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </div>
              ))}
            </div>
          </div>
        )}

        <ConfirmDeleteDialog
          target={pendingCredsDelete}
          onClose={() => {
            setPendingCredsDelete(null);
          }}
          onConfirm={handleDelete}
          title={(c) =>
            `Delete SSH credentials for ${c.username} on port ${String(c.port)}?`
          }
          // Every SSH path resolves through rolling.NodeSSHConfig, which
          // reads the one stored row and returns ErrSSHNotConfigured without
          // it: RunNodeCommand (maintenance mode, node sensors) fails,
          // CreateJob refuses auto_upgrade, and a running automated job fails
          // its next node before draining it (startNode's sshReady), which
          // fails the whole job. The DELETE removes only
          // cluster_ssh_credentials — the handler's re-home check notes the
          // pins outlive it.
          description={() =>
            "The stored password or private key is deleted from Nexara and cannot be recovered; you would have to enter it again. Until you do, Nexara cannot run commands on this cluster's nodes over SSH: rolling updates cannot run automated apt upgrades (a running rolling update with automated upgrades fails when its next node starts, before that node is drained), and node maintenance mode and node sensor readings stop working. Pinned host keys are kept."
          }
        />

        <ConfirmDeleteDialog
          target={pendingUnpin}
          onClose={() => {
            setPendingUnpin(null);
          }}
          onConfirm={(kh) => {
            deleteKnownHost.mutate({ clusterId, id: kh.id });
          }}
          title={(kh) =>
            `Unpin the host key for ${kh.host}:${String(kh.port)}?`
          }
          // Unpinning does NOT fall back to trusting whatever key is presented:
          // internal/ssh/client.go Execute refuses to connect when
          // KnownHostKey is nil, and rolling.NodeSSHConfig, which every SSH
          // path resolves through, returns ErrHostKeyNotPinned before
          // dialling, so the orchestrator fails the node (before its drain,
          // if it has not started) and the job. The pin is looked up by
          // (host, the credential's port) in NodeSSHConfig, so it is
          // host:port that is refused. A key is only pinned again
          // through Test Connection + Trust & Pin (PinSSHHostKey re-scans and
          // must match the fingerprint the operator confirmed).
          description={(kh) =>
            `Nexara forgets the pinned key ${kh.fingerprint}. It will not trust whatever key this host presents next: SSH connections to ${kh.host}:${String(kh.port)} are refused until you pin a key again with Test Connection, so automated upgrades, maintenance mode and sensor readings on that host stop working until then.`
          }
          confirmLabel="Unpin"
        />
      </>
    );
  }

  return (
    <div className="max-w-xl space-y-4 pt-1">
      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(11rem,100%),1fr))] gap-3">
        <div className="space-y-1.5">
          <Label htmlFor="ssh-user">Username</Label>
          <Input
            id="ssh-user"
            value={username}
            onChange={(e) => {
              setUsername(e.target.value);
            }}
            placeholder="root"
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="ssh-port">Port</Label>
          <Input
            id="ssh-port"
            type="number"
            value={port}
            onChange={(e) => {
              setPort(Number(e.target.value));
            }}
          />
        </div>
      </div>

      <div className="space-y-1.5">
        <Label>Sign in with</Label>
        <div>
          <Segmented
            aria-label="Sign in with"
            size="md"
            value={authType}
            onValueChange={setAuthType}
            options={[
              { value: "password", label: "Password" },
              { value: "key", label: "SSH key" },
            ]}
          />
        </div>
      </div>

      {authType === "password" ? (
        <div className="space-y-1.5">
          <Label htmlFor="ssh-password">Password</Label>
          <Input
            id="ssh-password"
            type="password"
            value={password}
            onChange={(e) => {
              setPassword(e.target.value);
            }}
            placeholder={creds ? "(unchanged)" : "Enter password"}
          />
        </div>
      ) : (
        <div className="space-y-1.5">
          <Label htmlFor="ssh-key">Private key (PEM format)</Label>
          <textarea
            id="ssh-key"
            className="w-full rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
            rows={6}
            value={privateKey}
            onChange={(e) => {
              setPrivateKey(e.target.value);
            }}
            placeholder={
              creds?.has_key
                ? "(unchanged)"
                : "-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----"
            }
          />
        </div>
      )}

      <SettingsFooter className="justify-start pt-0">
        <Button
          variant="outline"
          size="sm"
          onClick={handleSave}
          disabled={
            upsert.isPending ||
            (authType === "password" && !password && !creds) ||
            (authType === "key" && !privateKey && !creds?.has_key)
          }
        >
          {upsert.isPending && (
            <Loader2 className="mr-1 h-3 w-3 animate-spin" />
          )}
          Save
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            setEditing(false);
          }}
        >
          Cancel
        </Button>
      </SettingsFooter>
    </div>
  );
}
