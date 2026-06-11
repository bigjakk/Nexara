import { useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Loader2,
  CheckCircle,
  XCircle,
  Trash2,
  Key,
  ShieldAlert,
  ShieldCheck,
} from "lucide-react";
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
  SSHHostKeyPending,
  SSHHostKeyMismatch,
} from "@/types/api";
import { BulkPinDialog } from "./BulkPinDialog";

interface SSHCredentialsFormProps {
  clusterId: string;
}

type TestUiState =
  | { kind: "idle" }
  | { kind: "success"; fingerprint?: string }
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
  const { data: creds, isLoading } = useSSHCredentials(clusterId);
  const { data: nodes } = useClusterNodes(clusterId);
  const { data: knownHosts } = useSSHKnownHosts(clusterId);
  const upsert = useUpsertSSHCredentials();
  const deleteCreds = useDeleteSSHCredentials();
  const testConn = useTestSSHConnection();
  const pinKey = usePinSSHHostKey();
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
    testConn.mutate(
      { clusterId, nodeName },
      {
        onSuccess: (result) => {
          if (result.success) {
            setTestState(
              result.fingerprint !== undefined
                ? { kind: "success", fingerprint: result.fingerprint }
                : { kind: "success" },
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
        onError: () => {
          setTestState({ kind: "error", message: "Request failed" });
        },
      },
    );
  };

  const handlePin = (nodeName: string, expectedFingerprint: string) => {
    pinKey.mutate(
      { clusterId, nodeName, expectedFingerprint },
      {
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
      },
    );
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
        <Loader2 className="h-5 w-5 animate-spin" />
      </div>
    );
  }

  if (!editing && !creds) {
    return (
      <div className="rounded-md border border-dashed p-4 text-center">
        <Key className="mx-auto mb-2 h-8 w-8 text-muted-foreground" />
        <p className="mb-2 text-sm text-muted-foreground">
          No SSH credentials configured. Add credentials to enable automated{" "}
          <code className="text-xs">apt dist-upgrade</code> during rolling
          updates.
        </p>
        <Button size="sm" onClick={startEditing}>
          Configure SSH
        </Button>
      </div>
    );
  }

  if (!editing && creds) {
    return (
      <div className="space-y-3">
        <div className="flex items-center justify-between rounded-md border p-3">
          <div className="space-y-1 text-sm">
            <p>
              <span className="text-muted-foreground">User:</span>{" "}
              <span className="font-medium">{creds.username}</span>
            </p>
            <p>
              <span className="text-muted-foreground">Port:</span>{" "}
              <span className="font-medium">{creds.port}</span>
            </p>
            <p>
              <span className="text-muted-foreground">Auth:</span>{" "}
              <span className="font-medium">
                {creds.auth_type === "key" ? "SSH Key" : "Password"}
              </span>
            </p>
          </div>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={startEditing}>
              Edit
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={handleDelete}
              disabled={deleteCreds.isPending}
            >
              <Trash2 className="h-4 w-4 text-destructive" />
            </Button>
          </div>
        </div>

        {pinSummary.total > 0 && (
          <div className="flex items-center justify-between rounded-md border bg-muted/30 px-3 py-2 text-xs">
            <div className="flex items-center gap-2">
              {pinSummary.pinned === pinSummary.total ? (
                <ShieldCheck className="h-4 w-4 text-emerald-500" />
              ) : (
                <ShieldAlert className="h-4 w-4 text-amber-500" />
              )}
              <span>
                Host keys:{" "}
                <span className="font-medium">
                  {pinSummary.pinned}/{pinSummary.total}
                </span>{" "}
                pinned
                {pinSummary.pinned < pinSummary.total && (
                  <span className="ml-1 text-muted-foreground">
                    — rolling updates require all nodes pinned
                  </span>
                )}
              </span>
            </div>
            {unpinnedNodes.length > 0 && (
              <Button
                size="sm"
                variant="default"
                onClick={() => {
                  setBulkOpen(true);
                }}
              >
                Pin all node host keys
              </Button>
            )}
          </div>
        )}

        <div className="flex items-center gap-2">
          <select
            className="h-9 rounded-md border bg-background px-3 text-sm"
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
            Test Connection
          </Button>
        </div>

        <BulkPinDialog
          open={bulkOpen}
          onOpenChange={setBulkOpen}
          clusterId={clusterId}
          nodes={(nodes ?? []).map((n) => ({
            name: n.name,
            address: n.address,
          }))}
        />

        {testState.kind === "success" && (
          <div className="flex items-center gap-2 rounded-md border border-emerald-500/50 bg-emerald-500/10 p-2 text-xs text-emerald-600 dark:text-emerald-400">
            <CheckCircle className="h-4 w-4 flex-shrink-0" />
            <div>
              SSH connection successful.
              {testState.fingerprint && (
                <span className="ml-1 font-mono text-[10px] opacity-80">
                  ({testState.fingerprint})
                </span>
              )}
            </div>
          </div>
        )}

        {testState.kind === "error" && (
          <div className="flex items-start gap-2 rounded-md border border-destructive/50 bg-destructive/10 p-2 text-xs text-destructive">
            <XCircle className="mt-0.5 h-4 w-4 flex-shrink-0" />
            <div className="break-words">{testState.message}</div>
          </div>
        )}

        {testState.kind === "pending" && (
          <div className="space-y-2 rounded-md border border-amber-500/50 bg-amber-500/10 p-3 text-xs">
            <div className="flex items-start gap-2 text-amber-700 dark:text-amber-400">
              <ShieldAlert className="mt-0.5 h-4 w-4 flex-shrink-0" />
              <div>
                <p className="font-medium">
                  Host key not yet trusted.
                </p>
                <p className="mt-1 opacity-90">
                  Compare this fingerprint to the value reported by the node
                  itself (run{" "}
                  <code className="text-[11px]">
                    ssh-keyscan {testState.info.host}
                  </code>{" "}
                  on a trusted machine, or check the node console).
                </p>
              </div>
            </div>
            <div className="ml-6 space-y-1 font-mono text-[11px]">
              <div>
                <span className="text-muted-foreground">host: </span>
                {testState.info.host}:{testState.info.port}
              </div>
              <div className="break-all">
                <span className="text-muted-foreground">fingerprint: </span>
                {testState.info.fingerprint}
              </div>
            </div>
            <div className="ml-6 flex gap-2">
              <Button
                size="sm"
                variant="default"
                disabled={pinKey.isPending}
                onClick={() => {
                  handlePin(
                    testState.nodeName,
                    testState.info.fingerprint,
                  );
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
          <div className="space-y-2 rounded-md border border-destructive/60 bg-destructive/10 p-3 text-xs">
            <div className="flex items-start gap-2 text-destructive">
              <ShieldAlert className="mt-0.5 h-4 w-4 flex-shrink-0" />
              <div>
                <p className="font-medium">
                  Host key has changed since it was pinned.
                </p>
                <p className="mt-1 opacity-90">
                  This may be a legitimate reinstall or it may indicate a
                  man-in-the-middle. Investigate before re-pinning.
                </p>
              </div>
            </div>
            <div className="ml-6 space-y-1 font-mono text-[11px]">
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
            <div className="ml-6 flex gap-2">
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
          <div className="rounded-md border p-3">
            <div className="mb-2 flex items-center gap-2 text-xs font-medium">
              <ShieldCheck className="h-4 w-4 text-emerald-600 dark:text-emerald-400" />
              Pinned host keys
            </div>
            <div className="space-y-1.5">
              {knownHosts.map((kh) => (
                <div
                  key={kh.id}
                  className="flex items-center justify-between gap-2 rounded border bg-muted/30 px-2 py-1.5 text-xs"
                >
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-mono">
                      {kh.host}:{kh.port}
                    </div>
                    <div className="truncate font-mono text-[10px] text-muted-foreground">
                      {kh.fingerprint}
                    </div>
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={deleteKnownHost.isPending}
                    onClick={() => {
                      deleteKnownHost.mutate({ clusterId, id: kh.id });
                    }}
                    title="Unpin host key"
                  >
                    <Trash2 className="h-3.5 w-3.5 text-destructive" />
                  </Button>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="space-y-3 rounded-md border p-3">
      <div className="grid grid-cols-2 gap-3">
        <div>
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
        <div>
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

      <div>
        <Label>Auth Type</Label>
        <div className="flex gap-2 mt-1">
          <Button
            variant={authType === "password" ? "default" : "outline"}
            size="sm"
            onClick={() => {
              setAuthType("password");
            }}
          >
            Password
          </Button>
          <Button
            variant={authType === "key" ? "default" : "outline"}
            size="sm"
            onClick={() => {
              setAuthType("key");
            }}
          >
            SSH Key
          </Button>
        </div>
      </div>

      {authType === "password" ? (
        <div>
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
        <div>
          <Label htmlFor="ssh-key">Private Key (PEM format)</Label>
          <textarea
            id="ssh-key"
            className="w-full rounded-md border bg-background px-3 py-2 text-xs font-mono"
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

      <div className="flex justify-end gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            setEditing(false);
          }}
        >
          Cancel
        </Button>
        <Button
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
      </div>
    </div>
  );
}
