import { useEffect, useState } from "react";
import { Download } from "lucide-react";
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
import { Checkbox } from "@/components/ui/checkbox";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { TaskProgressBanner } from "@/features/vms/components/TaskProgressBanner";
import { useDownloadURL } from "../api/storage-queries";

interface URLDownloadDialogProps {
  clusterId: string;
  storageId: string;
  supportedContent: string;
}

type ContentType = "iso" | "vztmpl" | "import";

export function URLDownloadDialog({
  clusterId,
  storageId,
  supportedContent,
}: URLDownloadDialogProps) {
  const supportsIso = supportedContent.includes("iso");
  const supportsVztmpl = supportedContent.includes("vztmpl");
  const supportsImport = supportedContent.includes("import");

  const initialContent: ContentType = supportsIso
    ? "iso"
    : supportsVztmpl
      ? "vztmpl"
      : "import";

  const [open, setOpen] = useState(false);
  const [content, setContent] = useState<ContentType>(initialContent);
  const [url, setUrl] = useState("");
  const [filename, setFilename] = useState("");
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [checksum, setChecksum] = useState("");
  const [checksumAlgorithm, setChecksumAlgorithm] = useState<"sha256" | "sha512" | "sha1" | "md5">("sha256");
  const [decompression, setDecompression] = useState<"" | "gz" | "lzo" | "zst" | "bz2">("");
  const [verifyCerts, setVerifyCerts] = useState(true);
  const [taskUpid, setTaskUpid] = useState<string | null>(null);
  const downloadMutation = useDownloadURL();

  const urlTrimmed = url.trim();
  const filenameTrimmed = filename.trim();
  const urlValid =
    urlTrimmed.length > 0 &&
    urlTrimmed.length <= 2048 &&
    /^https?:\/\//i.test(urlTrimmed);
  const filenameValid =
    filenameTrimmed.length > 0 &&
    filenameTrimmed.length <= 255 &&
    !filenameTrimmed.includes("/") &&
    !filenameTrimmed.includes("\\") &&
    !filenameTrimmed.includes("..");

  function deriveFilename(input: string) {
    try {
      const u = new URL(input);
      const segments = u.pathname.split("/").filter(Boolean);
      return segments.length > 0 ? (segments[segments.length - 1] ?? "") : "";
    } catch {
      return "";
    }
  }

  function handleUrlBlur() {
    if (!filename && urlValid) {
      const derived = deriveFilename(urlTrimmed);
      if (derived) setFilename(derived);
    }
  }

  function handleSubmit() {
    if (!urlValid || !filenameValid) return;
    setTaskUpid(null);
    downloadMutation.mutate(
      {
        clusterId,
        storageId,
        data: {
          url: urlTrimmed,
          content,
          filename: filenameTrimmed,
          ...(checksum.trim() && {
            checksum: checksum.trim(),
            checksum_algorithm: checksumAlgorithm,
          }),
          ...(decompression && { decompression_algorithm: decompression }),
          ...(!verifyCerts && { verify_certificates: false }),
        },
      },
      {
        onSuccess: (data) => {
          if (data.upid) setTaskUpid(data.upid);
        },
      },
    );
  }

  function handleReset() {
    setUrl("");
    setFilename("");
    setChecksum("");
    setChecksumAlgorithm("sha256");
    setDecompression("");
    setVerifyCerts(true);
    setShowAdvanced(false);
    setTaskUpid(null);
    setContent(initialContent);
    downloadMutation.reset();
  }

  useEffect(() => {
    if (!open) handleReset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  if (!supportsIso && !supportsVztmpl && !supportsImport) return null;

  const isBusy = downloadMutation.isPending || taskUpid !== null;

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
          <Download className="mr-2 h-4 w-4" />
          Download URL
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Download from URL</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>Content type</Label>
            <div className="flex gap-2 flex-wrap">
              {supportsIso && (
                <Button
                  size="sm"
                  variant={content === "iso" ? "default" : "outline"}
                  onClick={() => { setContent("iso"); }}
                  disabled={isBusy}
                >
                  ISO Image
                </Button>
              )}
              {supportsVztmpl && (
                <Button
                  size="sm"
                  variant={content === "vztmpl" ? "default" : "outline"}
                  onClick={() => { setContent("vztmpl"); }}
                  disabled={isBusy}
                >
                  CT Template
                </Button>
              )}
              {supportsImport && (
                <Button
                  size="sm"
                  variant={content === "import" ? "default" : "outline"}
                  onClick={() => { setContent("import"); }}
                  disabled={isBusy}
                >
                  Import
                </Button>
              )}
            </div>
          </div>

          <div className="space-y-2">
            <Label htmlFor="url-input">URL</Label>
            <Input
              id="url-input"
              placeholder="https://example.com/file.iso"
              value={url}
              onChange={(e) => { setUrl(e.target.value); }}
              onBlur={handleUrlBlur}
              disabled={isBusy}
              autoComplete="off"
              spellCheck={false}
            />
            {url !== "" && !urlValid && (
              <p className="text-xs text-destructive">
                Must be an http(s) URL up to 2048 characters.
              </p>
            )}
          </div>

          <div className="space-y-2">
            <Label htmlFor="url-filename">Filename</Label>
            <Input
              id="url-filename"
              placeholder="file.iso"
              value={filename}
              onChange={(e) => { setFilename(e.target.value); }}
              disabled={isBusy}
              autoComplete="off"
              spellCheck={false}
            />
            {filename !== "" && !filenameValid && (
              <p className="text-xs text-destructive">
                Filename must be ≤255 chars; no <code>/</code>, <code>\</code>, or <code>..</code>.
              </p>
            )}
          </div>

          <button
            type="button"
            onClick={() => { setShowAdvanced((v) => !v); }}
            className="text-xs text-muted-foreground hover:text-foreground"
            disabled={isBusy}
          >
            {showAdvanced ? "Hide" : "Show"} advanced options
          </button>

          {showAdvanced && (
            <div className="space-y-3 rounded-md border border-border p-3">
              <div className="grid grid-cols-3 gap-2">
                <div className="col-span-2 space-y-1">
                  <Label htmlFor="checksum">Checksum</Label>
                  <Input
                    id="checksum"
                    placeholder="hex-encoded digest"
                    value={checksum}
                    onChange={(e) => { setChecksum(e.target.value); }}
                    disabled={isBusy}
                    autoComplete="off"
                    spellCheck={false}
                  />
                </div>
                <div className="space-y-1">
                  <Label>Algorithm</Label>
                  <Select
                    value={checksumAlgorithm}
                    onValueChange={(v) => { setChecksumAlgorithm(v as typeof checksumAlgorithm); }}
                    disabled={isBusy}
                  >
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="sha256">SHA-256</SelectItem>
                      <SelectItem value="sha512">SHA-512</SelectItem>
                      <SelectItem value="sha1">SHA-1</SelectItem>
                      <SelectItem value="md5">MD5</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </div>
              <div className="space-y-1">
                <Label>Decompression</Label>
                <Select
                  value={decompression || "none"}
                  onValueChange={(v) => {
                    setDecompression(v === "none" ? "" : (v as typeof decompression));
                  }}
                  disabled={isBusy}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="none">None</SelectItem>
                    <SelectItem value="gz">gzip</SelectItem>
                    <SelectItem value="zst">zstd</SelectItem>
                    <SelectItem value="bz2">bzip2</SelectItem>
                    <SelectItem value="lzo">lzo</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="flex items-center gap-2">
                <Checkbox
                  id="verify-certs"
                  checked={verifyCerts}
                  onCheckedChange={(v) => { setVerifyCerts(v === true); }}
                  disabled={isBusy}
                />
                <Label htmlFor="verify-certs" className="text-sm font-normal">
                  Verify TLS certificates
                </Label>
              </div>
            </div>
          )}

          {taskUpid && (
            <TaskProgressBanner
              clusterId={clusterId}
              upid={taskUpid}
              description={`Download ${filenameTrimmed}`}
              onComplete={() => {
                setTaskUpid(null);
                setOpen(false);
              }}
            />
          )}

          {!taskUpid && (
            <Button
              onClick={handleSubmit}
              disabled={!urlValid || !filenameValid || isBusy}
              className="w-full"
            >
              {downloadMutation.isPending ? "Dispatching…" : "Download"}
            </Button>
          )}

          {downloadMutation.isError && (
            <p className="text-sm text-destructive">
              {downloadMutation.error instanceof Error
                ? downloadMutation.error.message
                : "Download failed"}
            </p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
