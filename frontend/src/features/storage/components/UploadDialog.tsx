import { useState, useRef, useCallback } from "react";
import { Upload } from "lucide-react";
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
      { clusterId, storageId, content: contentType, file: selectedFile, onProgress },
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
    <Dialog open={open} onOpenChange={(v) => {
      if (!v && isBusy) return; // prevent closing during upload/copy
      setOpen(v);
      if (!v) {
        handleReset();
      }
    }}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline">
          <Upload className="mr-2 h-4 w-4" />
          Upload
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Upload to Storage</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>Content Type</Label>
            <div className="flex gap-2">
              {supportsIso && (
                <Button
                  size="sm"
                  variant={contentType === "iso" ? "default" : "outline"}
                  onClick={() => { setContentType("iso"); }}
                  disabled={isBusy}
                >
                  ISO Image
                </Button>
              )}
              {supportsVztmpl && (
                <Button
                  size="sm"
                  variant={contentType === "vztmpl" ? "default" : "outline"}
                  onClick={() => { setContentType("vztmpl"); }}
                  disabled={isBusy}
                >
                  CT Template
                </Button>
              )}
            </div>
          </div>
          <div className="space-y-2">
            <Label htmlFor="upload-file">File</Label>
            <Input
              id="upload-file"
              ref={fileInputRef}
              type="file"
              accept={contentType === "iso" ? ".iso,.img" : ".tar.gz,.tar.xz,.tar.zst"}
              onChange={(e) => { setSelectedFile(e.target.files?.[0] ?? null); }}
              disabled={isBusy}
            />
          </div>
          {selectedFile && !isBusy && (
            <p className="text-xs text-muted-foreground">
              {selectedFile.name} ({(selectedFile.size / 1024 / 1024).toFixed(1)} MB)
            </p>
          )}

          {/* Upload progress (browser → ProxDash → Proxmox) */}
          {progress !== null && (
            <div className="space-y-1">
              <div className="flex justify-between text-xs text-muted-foreground">
                <span>Uploading to Proxmox...</span>
                <span>{String(progress)}%</span>
              </div>
              <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
                <div
                  className="h-full rounded-full bg-primary transition-all duration-300"
                  style={{ width: `${String(progress)}%` }}
                />
              </div>
            </div>
          )}

          {/* Proxmox task progress (file import/copy on the node) */}
          {taskUpid && (
            <TaskProgressBanner
              clusterId={clusterId}
              upid={taskUpid}
              kind="vm"
              resourceId={storageId}
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
