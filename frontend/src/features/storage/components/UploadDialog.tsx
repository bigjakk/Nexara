import { useState, useRef, useCallback } from "react";
import { Upload } from "lucide-react";
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
import { Segmented } from "@/components/ui/segmented";
import { UsageBar } from "@/components/UsageBar";
import { formatBytes } from "@/lib/format";
import { TaskProgressBanner } from "@/features/vms/components/TaskProgressBanner";
import { useUploadFile } from "../api/storage-queries";

interface UploadDialogProps {
  clusterId: string;
  storageId: string;
  supportedContent: string;
}

type ContentType = "iso" | "vztmpl";

export function UploadDialog({
  clusterId,
  storageId,
  supportedContent,
}: UploadDialogProps) {
  const [open, setOpen] = useState(false);
  const [contentType, setContentType] = useState<ContentType>("iso");
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [progress, setProgress] = useState<number | null>(null);
  const [taskUpid, setTaskUpid] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const uploadMutation = useUploadFile();
  const queryClient = useQueryClient();

  const supportsIso = supportedContent.includes("iso");
  const supportsVztmpl = supportedContent.includes("vztmpl");

  const onProgress = useCallback((percent: number) => {
    setProgress(percent);
  }, []);

  if (!supportsIso && !supportsVztmpl) return null;

  function handleUpload() {
    if (!selectedFile) return;
    setProgress(0);
    setTaskUpid(null);
    uploadMutation.mutate(
      {
        clusterId,
        storageId,
        content: contentType,
        file: selectedFile,
        onProgress,
      },
      {
        onSuccess: (data) => {
          setProgress(null);
          if (data.upid) {
            setTaskUpid(data.upid);
          }
        },
        onError: () => {
          setProgress(null);
        },
      },
    );
  }

  function handleTaskComplete() {
    void queryClient.invalidateQueries({
      queryKey: ["clusters", clusterId, "storage", storageId, "content"],
    });
    setTaskUpid(null);
    setOpen(false);
    setSelectedFile(null);
    if (fileInputRef.current) fileInputRef.current.value = "";
  }

  function handleReset() {
    setProgress(null);
    setTaskUpid(null);
    setSelectedFile(null);
    uploadMutation.reset();
    if (fileInputRef.current) fileInputRef.current.value = "";
  }

  const isBusy = uploadMutation.isPending || taskUpid !== null;

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v && isBusy) return; // prevent closing during upload/copy
        setOpen(v);
        if (!v) {
          handleReset();
        }
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm" className="gap-1.5">
          <Upload className="h-4 w-4" />
          Upload
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Upload to storage</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>Content type</Label>
            <div>
              <Segmented<ContentType>
                aria-label="Content type"
                size="md"
                value={contentType}
                onValueChange={setContentType}
                options={[
                  ...(supportsIso
                    ? [
                        {
                          value: "iso" as const,
                          label: "ISO image",
                          disabled: isBusy,
                        },
                      ]
                    : []),
                  ...(supportsVztmpl
                    ? [
                        {
                          value: "vztmpl" as const,
                          label: "CT template",
                          disabled: isBusy,
                        },
                      ]
                    : []),
                ]}
              />
            </div>
          </div>
          <div className="space-y-2">
            <Label htmlFor="upload-file">File</Label>
            <Input
              id="upload-file"
              ref={fileInputRef}
              type="file"
              accept={
                contentType === "iso" ? ".iso,.img" : ".tar.gz,.tar.xz,.tar.zst"
              }
              onChange={(e) => {
                setSelectedFile(e.target.files?.[0] ?? null);
              }}
              disabled={isBusy}
            />
          </div>
          {selectedFile && !isBusy && (
            <p className="text-xs text-muted-foreground">
              {selectedFile.name} ({formatBytes(selectedFile.size)})
            </p>
          )}

          {/* Upload progress (browser → Nexara → Proxmox) */}
          {progress !== null && (
            <div className="space-y-1.5">
              <div className="flex justify-between text-[12.5px] tabular-nums text-muted-foreground">
                <span>Uploading to Proxmox...</span>
                <span>{String(progress)}%</span>
              </div>
              <UsageBar percent={progress} thresholds={false} />
            </div>
          )}

          {/* Proxmox task progress (file import/copy on the node) */}
          {taskUpid && (
            <TaskProgressBanner
              clusterId={clusterId}
              upid={taskUpid}
              description={`Upload: ${selectedFile?.name ?? "file"}`}
              onComplete={handleTaskComplete}
            />
          )}

          {!taskUpid && (
            <Button
              onClick={handleUpload}
              disabled={!selectedFile || isBusy}
              className="w-full"
            >
              {uploadMutation.isPending ? "Uploading..." : "Upload"}
            </Button>
          )}
          {uploadMutation.isError && (
            <p className="text-sm text-destructive">
              {uploadMutation.error instanceof Error
                ? uploadMutation.error.message
                : "Upload failed"}
            </p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
