import { useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Key,
  Plus,
  Copy,
  Check,
  Trash2,
  ExternalLink,
  AlertTriangle,
  Loader2,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ApiClientError } from "@/lib/api-client";
import {
  useAPIKeys,
  useCreateAPIKey,
  useRevokeAPIKey,
} from "../api/api-key-queries";

type ExpiryOption = "never" | "30" | "90" | "365";

const EXPIRY_LABELS: Record<ExpiryOption, string> = {
  never: "Never",
  "30": "30 days",
  "90": "90 days",
  "365": "1 year",
};

function getExpirySeconds(option: ExpiryOption): number | undefined {
  if (option === "never") return undefined;
  return Number(option) * 86400; // days to seconds
}

function getKeyStatus(key: { is_revoked: boolean; expires_at: string | null }): {
  label: string;
  variant: "default" | "secondary" | "destructive";
} {
  if (key.is_revoked) {
    return { label: "Revoked", variant: "secondary" };
  }
  if (key.expires_at && new Date(key.expires_at) < new Date()) {
    return { label: "Expired", variant: "destructive" };
  }
  return { label: "Active", variant: "default" };
}

function formatDate(dateStr: string | null): string {
  if (!dateStr) return "Never";
  return new Date(dateStr).toLocaleDateString();
}

export function APIKeysPage() {
  const navigate = useNavigate();
  const { data: keys, isLoading } = useAPIKeys();
  const createMutation = useCreateAPIKey();
  const revokeMutation = useRevokeAPIKey();

  // Create dialog state
  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [keyName, setKeyName] = useState("");
  const [expiry, setExpiry] = useState<ExpiryOption>("never");
  const [createError, setCreateError] = useState("");

  // Key reveal dialog state
  const [revealDialogOpen, setRevealDialogOpen] = useState(false);
  const [createdKey, setCreatedKey] = useState("");
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState("");

  // Revoke confirmation state
  const [revokeDialogOpen, setRevokeDialogOpen] = useState(false);
  const [revokeTargetId, setRevokeTargetId] = useState<string | null>(null);
  const [revokeTargetName, setRevokeTargetName] = useState("");

  const resetCreateDialog = () => {
    setKeyName("");
    setExpiry("never");
    setCreateError("");
  };

  const handleCreate = async () => {
    setCreateError("");
    try {
      const res = await createMutation.mutateAsync({
        name: keyName.trim(),
        expires_in: getExpirySeconds(expiry),
      });
      setCreatedKey(res.key);
      setCreateDialogOpen(false);
      resetCreateDialog();
      setRevealDialogOpen(true);
    } catch (err) {
      if (err instanceof ApiClientError) {
        setCreateError(err.message);
      } else {
        setCreateError("Failed to create API key");
      }
    }
  };

  const handleCopy = async () => {
    setCopyError("");
    if (!navigator.clipboard || !window.isSecureContext) {
      setCopyError("Clipboard requires HTTPS. Please select the key above and copy manually (Ctrl+C).");
      return;
    }
    try {
      await navigator.clipboard.writeText(createdKey);
      setCopied(true);
      setTimeout(() => {
        setCopied(false);
      }, 2000);
    } catch {
      setCopyError("Copy failed. Please select the key above and copy manually (Ctrl+C).");
    }
  };

  const handleRevoke = async () => {
    if (!revokeTargetId) return;
    try {
      await revokeMutation.mutateAsync(revokeTargetId);
      setRevokeDialogOpen(false);
      setRevokeTargetId(null);
      setRevokeTargetName("");
    } catch {
      // mutation error state is available via revokeMutation.isError
    }
  };

  if (isLoading) {
    return (
      <div className="flex h-64 items-center justify-center">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold">API Keys</h1>
          <p className="text-sm text-muted-foreground">
            Create and manage personal API keys for programmatic access to the
            Nexara API
          </p>
        </div>
        <div className="flex gap-2">
          <Button
            variant="outline"
            onClick={() => {
              void navigate("/settings/api-docs");
            }}
          >
            <ExternalLink className="mr-2 h-4 w-4" />
            View API Documentation
          </Button>
          <Button
            onClick={() => {
              resetCreateDialog();
              setCreateDialogOpen(true);
            }}
          >
            <Plus className="mr-2 h-4 w-4" />
            Create API Key
          </Button>
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Key className="h-5 w-5" />
            Your API Keys
          </CardTitle>
          <CardDescription>
            API keys allow programmatic access to the Nexara API on your behalf.
            Keep your keys secure and revoke any that are no longer needed.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {!keys || keys.length === 0 ? (
            <div className="flex flex-col items-center justify-center rounded-lg border border-dashed py-12 text-center">
              <Key className="mb-3 h-10 w-10 text-muted-foreground" />
              <p className="text-sm font-medium">No API keys yet</p>
              <p className="mt-1 text-sm text-muted-foreground">
                Create an API key to get started with programmatic access.
              </p>
              <Button
                className="mt-4"
                size="sm"
                onClick={() => {
                  resetCreateDialog();
                  setCreateDialogOpen(true);
                }}
              >
                <Plus className="mr-2 h-4 w-4" />
                Create your first API key
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Key Prefix</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead>Last Used</TableHead>
                  <TableHead>Expires</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {keys.map((apiKey) => {
                  const status = getKeyStatus(apiKey);
                  return (
                    <TableRow key={apiKey.id}>
                      <TableCell className="font-medium">
                        {apiKey.name}
                      </TableCell>
                      <TableCell>
                        <code className="rounded bg-muted px-1.5 py-0.5 text-xs font-mono">
                          {apiKey.key_prefix}...
                        </code>
                      </TableCell>
                      <TableCell className="text-sm text-muted-foreground">
                        {formatDate(apiKey.created_at)}
                      </TableCell>
                      <TableCell className="text-sm text-muted-foreground">
                        {formatDate(apiKey.last_used_at)}
                      </TableCell>
                      <TableCell className="text-sm text-muted-foreground">
                        {formatDate(apiKey.expires_at)}
                      </TableCell>
                      <TableCell>
                        <Badge variant={status.variant}>{status.label}</Badge>
                      </TableCell>
                      <TableCell className="text-right">
                        {!apiKey.is_revoked && (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-destructive hover:text-destructive"
                            onClick={() => {
                              setRevokeTargetId(apiKey.id);
                              setRevokeTargetName(apiKey.name);
                              setRevokeDialogOpen(true);
                            }}
                          >
                            <Trash2 className="mr-1 h-4 w-4" />
                            Revoke
                          </Button>
                        )}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {/* Create API Key Dialog */}
      <Dialog
        open={createDialogOpen}
        onOpenChange={(open) => {
          if (!open) resetCreateDialog();
          setCreateDialogOpen(open);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create API Key</DialogTitle>
            <DialogDescription>
              Create a new API key for programmatic access. Give it a
              descriptive name so you can identify it later.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="space-y-2">
              <Label htmlFor="key-name">Name</Label>
              <Input
                id="key-name"
                placeholder="e.g., CI/CD Pipeline, Monitoring Script"
                value={keyName}
                onChange={(e) => {
                  setKeyName(e.target.value);
                }}
                maxLength={100}
                autoFocus
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="key-expiry">Expiration</Label>
              <Select
                value={expiry}
                onValueChange={(val) => {
                  setExpiry(val as ExpiryOption);
                }}
              >
                <SelectTrigger id="key-expiry">
                  <SelectValue placeholder="Select expiration" />
                </SelectTrigger>
                <SelectContent>
                  {(
                    Object.entries(EXPIRY_LABELS) as [ExpiryOption, string][]
                  ).map(([value, label]) => (
                    <SelectItem key={value} value={value}>
                      {label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {createError && (
              <p className="text-sm text-destructive">{createError}</p>
            )}
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                setCreateDialogOpen(false);
              }}
            >
              Cancel
            </Button>
            <Button
              onClick={() => void handleCreate()}
              disabled={!keyName.trim() || createMutation.isPending}
            >
              {createMutation.isPending ? (
                <>
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                  Creating...
                </>
              ) : (
                "Create Key"
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Key Reveal Dialog */}
      <Dialog
        open={revealDialogOpen}
        onOpenChange={(open) => {
          if (!open) {
            setCreatedKey("");
            setCopied(false);
          }
          setRevealDialogOpen(open);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>API Key Created</DialogTitle>
            <DialogDescription>
              Your new API key has been created successfully.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 p-3 dark:border-amber-800 dark:bg-amber-950/20">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />
              <p className="text-sm text-amber-800 dark:text-amber-200">
                Make sure to copy your API key now. You won&apos;t be able to
                see it again!
              </p>
            </div>
            <div className="flex items-center gap-2">
              <code className="flex-1 rounded-md bg-muted px-3 py-2 font-mono text-sm break-all select-all">
                {createdKey}
              </code>
              <Button
                variant="outline"
                size="icon"
                className="shrink-0"
                onClick={handleCopy}
              >
                {copied ? (
                  <Check className="h-4 w-4 text-green-600" />
                ) : (
                  <Copy className="h-4 w-4" />
                )}
              </Button>
            </div>
            {copyError && (
              <p className="text-sm text-destructive">{copyError}</p>
            )}
          </div>
          <DialogFooter>
            <Button
              onClick={() => {
                setRevealDialogOpen(false);
                setCreatedKey("");
                setCopied(false);
                setCopyError("");
              }}
            >
              Done
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Revoke Confirmation Dialog */}
      <AlertDialog
        open={revokeDialogOpen}
        onOpenChange={(open: boolean) => {
          if (!open) {
            setRevokeTargetId(null);
            setRevokeTargetName("");
          }
          setRevokeDialogOpen(open);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Revoke API Key</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to revoke the API key &ldquo;
              {revokeTargetName}&rdquo;? Any applications using this key will
              lose access immediately. This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              disabled={revokeMutation.isPending}
              onClick={(e: React.MouseEvent) => {
                e.preventDefault();
                void handleRevoke();
              }}
            >
              {revokeMutation.isPending ? (
                <>
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                  Revoking...
                </>
              ) : (
                "Revoke Key"
              )}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
