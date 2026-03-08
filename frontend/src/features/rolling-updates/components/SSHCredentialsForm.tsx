import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Loader2, CheckCircle, XCircle, Trash2, Key } from "lucide-react";
import {
  useSSHCredentials,
  useUpsertSSHCredentials,
  useDeleteSSHCredentials,
  useTestSSHConnection,
} from "../api/rolling-update-queries";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";

interface SSHCredentialsFormProps {
  clusterId: string;
}

export function SSHCredentialsForm({ clusterId }: SSHCredentialsFormProps) {
  const { data: creds, isLoading } = useSSHCredentials(clusterId);
  const { data: nodes } = useClusterNodes(clusterId);
  const upsert = useUpsertSSHCredentials();
  const deleteCreds = useDeleteSSHCredentials();
  const testConn = useTestSSHConnection();

  const [editing, setEditing] = useState(false);
  const [username, setUsername] = useState("root");
  const [port, setPort] = useState(22);
  const [authType, setAuthType] = useState<"password" | "key">("password");
  const [password, setPassword] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [testNode, setTestNode] = useState("");
  const [testResult, setTestResult] = useState<{
    success: boolean;
    message: string;
  } | null>(null);

  const startEditing = () => {
    if (creds) {
      setUsername(creds.username);
      setPort(creds.port);
      setAuthType(creds.auth_type);
    }
    setPassword("");
    setPrivateKey("");
    setTestResult(null);
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
    upsert.mutate(
      body,
      {
        onSuccess: () => {
          setEditing(false);
          setPassword("");
          setPrivateKey("");
        },
      },
    );
  };

  const handleTest = () => {
    setTestResult(null);
    const firstNode = nodes?.[0];
    const nodeName = testNode || (firstNode ? firstNode.name : "");
    if (!nodeName) return;
    testConn.mutate(
      { clusterId, nodeName },
      {
        onSuccess: (result) => {
          setTestResult(result);
        },
        onError: () => {
          setTestResult({ success: false, message: "Request failed" });
        },
      },
    );
  };

  const handleDelete = () => {
    deleteCreds.mutate({ clusterId }, {
      onSuccess: () => {
        setEditing(false);
      },
    });
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

        <div className="flex items-center gap-2">
          <select
            className="h-9 rounded-md border bg-background px-3 text-sm"
            value={testNode}
            onChange={(e) => { setTestNode(e.target.value); }}
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
            disabled={testConn.isPending || (!testNode && (!nodes || nodes.length === 0))}
          >
            {testConn.isPending ? (
              <Loader2 className="mr-1 h-3 w-3 animate-spin" />
            ) : null}
            Test Connection
          </Button>
          {testResult && (
            <span
              className={`flex items-center gap-1 text-xs ${testResult.success ? "text-green-500" : "text-destructive"}`}
            >
              {testResult.success ? (
                <CheckCircle className="h-3 w-3" />
              ) : (
                <XCircle className="h-3 w-3" />
              )}
              {testResult.message}
            </span>
          )}
        </div>
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
            onChange={(e) => { setUsername(e.target.value); }}
            placeholder="root"
          />
        </div>
        <div>
          <Label htmlFor="ssh-port">Port</Label>
          <Input
            id="ssh-port"
            type="number"
            value={port}
            onChange={(e) => { setPort(Number(e.target.value)); }}
          />
        </div>
      </div>

      <div>
        <Label>Auth Type</Label>
        <div className="flex gap-2 mt-1">
          <Button
            variant={authType === "password" ? "default" : "outline"}
            size="sm"
            onClick={() => { setAuthType("password"); }}
          >
            Password
          </Button>
          <Button
            variant={authType === "key" ? "default" : "outline"}
            size="sm"
            onClick={() => { setAuthType("key"); }}
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
            onChange={(e) => { setPassword(e.target.value); }}
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
            onChange={(e) => { setPrivateKey(e.target.value); }}
            placeholder={creds?.has_key ? "(unchanged)" : "-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----"}
          />
        </div>
      )}

      <div className="flex justify-end gap-2">
        <Button variant="outline" size="sm" onClick={() => { setEditing(false); }}>
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
          {upsert.isPending && <Loader2 className="mr-1 h-3 w-3 animate-spin" />}
          Save
        </Button>
      </div>
    </div>
  );
}
