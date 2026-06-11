import { useState, useEffect } from "react";
import { Container } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { TaskProgressBanner } from "@/features/vms/components/TaskProgressBanner";
import { usePullOCIImage } from "../api/storage-queries";

interface OCIPullDialogProps {
  clusterId: string;
  storageId: string;
}

// Pragmatic client-side validator matching the backend regex. Catches obvious
// garbage; Proxmox itself enforces the strict Docker reference grammar.
const REFERENCE_PATTERN = /^[A-Za-z0-9._\-/:@]+$/;
const FILENAME_PATTERN = /^[A-Za-z0-9._-]+$/;

export function OCIPullDialog({ clusterId, storageId }: OCIPullDialogProps) {
  const [open, setOpen] = useState(false);
  const [reference, setReference] = useState("");
  const [filename, setFilename] = useState("");
  const [taskUpid, setTaskUpid] = useState<string | null>(null);
  const pullMutation = usePullOCIImage();
  const queryClient = useQueryClient();

  const referenceTrimmed = reference.trim();
  const filenameTrimmed = filename.trim();
  const referenceValid =
    referenceTrimmed.length > 0 &&
    referenceTrimmed.length <= 512 &&
    REFERENCE_PATTERN.test(referenceTrimmed);
  const filenameValid =
    filenameTrimmed.length === 0 ||
    (filenameTrimmed.length <= 64 && FILENAME_PATTERN.test(filenameTrimmed));

  function handleSubmit() {
    if (!referenceValid || !filenameValid) return;
    setTaskUpid(null);
    pullMutation.mutate(
      {
        clusterId,
        storageId,
        data: {
          reference: referenceTrimmed,
          ...(filenameTrimmed && { file_name: filenameTrimmed }),
        },
      },
      {
        onSuccess: (data) => {
          if (data.upid) {
            setTaskUpid(data.upid);
          }
        },
      },
    );
  }

  function handleReset() {
    setReference("");
    setFilename("");
    setTaskUpid(null);
    pullMutation.reset();
  }

  useEffect(() => {
    if (!open) {
      handleReset();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const isBusy = pullMutation.isPending || taskUpid !== null;

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v && isBusy) return;
        setOpen(v);
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm" variant="outline">
          <Container className="mr-2 h-4 w-4" />
          Pull OCI
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Pull from OCI Registry</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="rounded-md border border-amber-500/30 bg-amber-500/5 p-3 text-xs">
            <p className="font-medium text-amber-700 dark:text-amber-400">
              Tech preview (Proxmox VE 9.1+)
            </p>
            <p className="mt-1 text-muted-foreground">
              The node must have <code className="rounded bg-muted px-1">skopeo</code> installed.
              OCI layers are squashed on container create; in-place updates are not
              supported — re-create the container to apply image changes.
            </p>
          </div>

          <div className="space-y-2">
            <Label htmlFor="oci-reference">Image reference</Label>
            <Input
              id="oci-reference"
              placeholder="docker.io/library/nginx:latest"
              value={reference}
              onChange={(e) => {
                setReference(e.target.value);
              }}
              disabled={isBusy}
              autoComplete="off"
              spellCheck={false}
            />
            <p className="text-xs text-muted-foreground">
              Full Docker-style reference: <code>registry/path:tag</code>.
              Examples: <code>docker.io/library/hello-world:latest</code>,{" "}
              <code>ghcr.io/owner/repo:v1.2.3</code>.
            </p>
            {reference !== "" && !referenceValid && (
              <p className="text-xs text-destructive">
                Reference must be 1–512 chars and contain only{" "}
                <code>A-Z a-z 0-9 . _ - / : @</code>.
              </p>
            )}
          </div>

          <div className="space-y-2">
            <Label htmlFor="oci-filename">
              Filename <span className="text-muted-foreground">(optional)</span>
            </Label>
            <Input
              id="oci-filename"
              placeholder="auto-generated from reference"
              value={filename}
              onChange={(e) => {
                setFilename(e.target.value);
              }}
              disabled={isBusy}
              autoComplete="off"
              spellCheck={false}
            />
            <p className="text-xs text-muted-foreground">
              Server appends <code>.tar</code>. Leave blank to let Proxmox derive it
              from the reference.
            </p>
            {filename !== "" && !filenameValid && (
              <p className="text-xs text-destructive">
                Up to 64 chars, only <code>A-Z a-z 0-9 . _ -</code>.
              </p>
            )}
          </div>

          {taskUpid && (
            <TaskProgressBanner
              clusterId={clusterId}
              upid={taskUpid}
              description={`Pull ${referenceTrimmed}`}
              onComplete={() => {
                void queryClient.invalidateQueries({
                  queryKey: ["clusters", clusterId, "storage", storageId, "content"],
                });
                setTaskUpid(null);
                setOpen(false);
              }}
            />
          )}

          {!taskUpid && (
            <Button
              onClick={handleSubmit}
              disabled={!referenceValid || !filenameValid || isBusy}
              className="w-full"
            >
              {pullMutation.isPending ? "Dispatching…" : "Pull image"}
            </Button>
          )}

          {pullMutation.isError && (
            <p className="text-sm text-destructive">
              {pullMutation.error instanceof Error
                ? pullMutation.error.message
                : "Pull failed"}
            </p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
