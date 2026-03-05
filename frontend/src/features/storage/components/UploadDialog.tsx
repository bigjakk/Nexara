import { useState, useRef } from "react";
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
  const fileInputRef = useRef<HTMLInputElement>(null);
  const uploadMutation = useUploadFile();

  const supportsIso = supportedContent.includes("iso");
  const supportsVztmpl = supportedContent.includes("vztmpl");

  if (!supportsIso && !supportsVztmpl) return null;

  function handleUpload() {
    if (!selectedFile) return;
    uploadMutation.mutate(
      { clusterId, storageId, content: contentType, file: selectedFile },
      {
        onSuccess: () => {
          setOpen(false);
          setSelectedFile(null);
          if (fileInputRef.current) fileInputRef.current.value = "";
        },
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
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
                >
                  ISO Image
                </Button>
              )}
              {supportsVztmpl && (
                <Button
                  size="sm"
                  variant={contentType === "vztmpl" ? "default" : "outline"}
                  onClick={() => { setContentType("vztmpl"); }}
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
            />
          </div>
          {selectedFile && (
            <p className="text-xs text-muted-foreground">
              {selectedFile.name} ({(selectedFile.size / 1024 / 1024).toFixed(1)} MB)
            </p>
          )}
          <Button
            onClick={handleUpload}
            disabled={!selectedFile || uploadMutation.isPending}
            className="w-full"
          >
            {uploadMutation.isPending ? "Uploading..." : "Upload"}
          </Button>
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
