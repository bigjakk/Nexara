import { useEffect, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useNotificationChannels } from "@/features/alerts/api/alert-queries";
import { useEmailReportRun } from "../api/report-queries";
import type { ReportRun } from "@/types/api";

interface ReportEmailDialogProps {
  run: ReportRun | null;
  onOpenChange: (open: boolean) => void;
}

/**
 * Sends a finished run through an email channel: the digest as the body and
 * the report attached. Recipients override the channel's own list when given.
 */
export function ReportEmailDialog({
  run,
  onOpenChange,
}: ReportEmailDialogProps) {
  const open = run !== null;
  const { data: channels } = useNotificationChannels();
  const emailChannels =
    channels?.filter((c) => c.channel_type === "email") ?? [];
  const [channelId, setChannelId] = useState("");
  const [recipients, setRecipients] = useState("");
  const [withCSV, setWithCSV] = useState(false);
  const [sent, setSent] = useState(false);
  const send = useEmailReportRun();

  useEffect(() => {
    if (!open) return;
    send.reset();
    setSent(false);
    const only = emailChannels[0];
    if (emailChannels.length === 1 && only) setChannelId(only.id);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- reset on open only
  }, [open]);

  const parsedRecipients = recipients
    .split(/[,\s]+/)
    .map((r) => r.trim())
    .filter((r) => r !== "");

  const handleSend = () => {
    if (!run || !channelId) return;
    send.mutate(
      {
        id: run.id,
        channel_id: channelId,
        recipients: parsedRecipients,
        with_csv: withCSV,
      },
      {
        onSuccess: () => {
          setSent(true);
        },
      },
    );
  };

  const errorMessage = send.error instanceof Error ? send.error.message : "";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Email this report</DialogTitle>
        </DialogHeader>
        <div className="space-y-4 pt-2">
          <div className="space-y-2">
            <Label>Email channel</Label>
            <Select value={channelId} onValueChange={setChannelId}>
              <SelectTrigger>
                <SelectValue placeholder="Select email channel" />
              </SelectTrigger>
              <SelectContent>
                {emailChannels.map((ch) => (
                  <SelectItem key={ch.id} value={ch.id}>
                    {ch.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {emailChannels.length === 0 && (
              <p className="text-xs text-muted-foreground">
                No email channel is available to you. Channels are managed under
                Alerts → Channels; listing them needs the notification channel
                permission.
              </p>
            )}
          </div>
          <div className="space-y-2">
            <Label htmlFor="email-recipients">Recipients</Label>
            <Input
              id="email-recipients"
              placeholder="Leave blank for the channel's own recipients"
              value={recipients}
              onChange={(e) => {
                setRecipients(e.target.value);
              }}
            />
            <p className="text-xs text-muted-foreground">
              Comma-separated. The email carries a digest of the findings with
              the full report attached as HTML.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox
              id="email-with-csv"
              checked={withCSV}
              onCheckedChange={(v) => {
                setWithCSV(v === true);
              }}
            />
            <Label htmlFor="email-with-csv">Attach the CSV as well</Label>
          </div>
          {errorMessage && (
            <p className="text-sm text-destructive">{errorMessage}</p>
          )}
          {sent && <p className="text-sm text-emerald-600">Sent.</p>}
          <Button
            onClick={handleSend}
            disabled={!channelId || send.isPending || sent}
            className="w-full"
          >
            {send.isPending ? "Sending…" : sent ? "Sent" : "Send"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
