import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Plus } from "lucide-react";
import {
  useCreateNotificationChannel,
  useUpdateNotificationChannel,
} from "../api/alert-queries";
import { useUsers } from "@/features/admin/api/rbac-queries";
import type { ChannelType } from "@/types/api";

// expo_push is intentionally omitted from this list — push notifications
// are wired but disabled at the UI level because the mobile registration
// flow isn't shipping yet. To re-enable, add `{ value: "expo_push", label:
// "Mobile push (Expo)" }` and re-enable the validChannelTypes entry in
// internal/api/handlers/alerts.go. The user-picker render block + the
// buildConfig case below are kept as dead code so re-enabling is a single
// list-entry change.
const CHANNEL_TYPES: { value: ChannelType; label: string }[] = [
  { value: "email", label: "Email (SMTP)" },
  { value: "slack", label: "Slack" },
  { value: "discord", label: "Discord" },
  { value: "teams", label: "Microsoft Teams" },
  { value: "telegram", label: "Telegram" },
  { value: "webhook", label: "Generic Webhook" },
  { value: "pagerduty", label: "PagerDuty" },
];

interface ChannelFormProps {
  editId?: string;
  editName?: string;
  editType?: ChannelType;
  editEnabled?: boolean;
  onClose?: () => void;
}

export function ChannelForm({
  editId,
  editName,
  editType,
  editEnabled,
  onClose,
}: ChannelFormProps) {
  const isEdit = !!editId;
  const [open, setOpen] = useState(false);
  const [name, setName] = useState(editName ?? "");
  const [channelType, setChannelType] = useState<ChannelType>(
    editType ?? "email",
  );
  const [enabled, setEnabled] = useState(editEnabled ?? true);

  // Email fields
  const [smtpHost, setSmtpHost] = useState("");
  const [smtpPort, setSmtpPort] = useState("587");
  const [smtpUsername, setSmtpUsername] = useState("");
  const [smtpPassword, setSmtpPassword] = useState("");
  const [smtpFrom, setSmtpFrom] = useState("");
  const [smtpTo, setSmtpTo] = useState("");
  const [smtpTls, setSmtpTls] = useState(true);
  const [smtpSubjectTemplate, setSmtpSubjectTemplate] = useState("");

  // Slack fields
  const [slackWebhookUrl, setSlackWebhookUrl] = useState("");
  const [slackChannel, setSlackChannel] = useState("");
  const [slackUsername, setSlackUsername] = useState("Nexara");

  // Discord fields
  const [discordWebhookUrl, setDiscordWebhookUrl] = useState("");
  const [discordUsername, setDiscordUsername] = useState("Nexara Alerts");

  // Teams fields
  const [teamsWebhookUrl, setTeamsWebhookUrl] = useState("");

  // Telegram fields
  const [telegramBotToken, setTelegramBotToken] = useState("");
  const [telegramChatId, setTelegramChatId] = useState("");

  // Webhook fields
  const [webhookUrl, setWebhookUrl] = useState("");
  const [webhookMethod, setWebhookMethod] = useState("POST");
  const [webhookHeaders, setWebhookHeaders] = useState("");
  const [webhookBodyTemplate, setWebhookBodyTemplate] = useState("");

  // PagerDuty fields
  const [pdRoutingKey, setPdRoutingKey] = useState("");

  // Expo push fields — recipient is a Nexara user; the dispatcher fans out
  // to every mobile device that user has registered.
  const [expoPushUserId, setExpoPushUserId] = useState("");

  const createMutation = useCreateNotificationChannel();
  const updateMutation = useUpdateNotificationChannel();
  const usersQuery = useUsers();

  const buildConfig = (): Record<string, unknown> => {
    switch (channelType) {
      case "email":
        return {
          host: smtpHost,
          port: Number(smtpPort),
          username: smtpUsername,
          password: smtpPassword,
          from: smtpFrom,
          to: smtpTo
            .split(",")
            .map((s) => s.trim())
            .filter(Boolean),
          tls: smtpTls,
          subject_template: smtpSubjectTemplate || undefined,
        };
      case "slack":
        return {
          webhook_url: slackWebhookUrl,
          channel: slackChannel || undefined,
          username: slackUsername || undefined,
        };
      case "discord":
        return {
          webhook_url: discordWebhookUrl,
          username: discordUsername || undefined,
        };
      case "teams":
        return { webhook_url: teamsWebhookUrl };
      case "telegram":
        return {
          bot_token: telegramBotToken,
          chat_id: telegramChatId,
          parse_mode: "HTML",
        };
      case "webhook": {
        let headers: Record<string, string> = {};
        if (webhookHeaders.trim()) {
          try {
            headers = JSON.parse(webhookHeaders) as Record<string, string>;
          } catch {
            // ignore parse errors, validation will catch it
          }
        }
        return {
          url: webhookUrl,
          method: webhookMethod,
          headers,
          body_template: webhookBodyTemplate || undefined,
        };
      }
      case "pagerduty":
        return { routing_key: pdRoutingKey };
      case "expo_push":
        return { user_id: expoPushUserId };
      default:
        return {};
    }
  };

  const handleSubmit = (e: React.SyntheticEvent) => {
    e.preventDefault();
    const config = buildConfig();

    if (isEdit && editId) {
      updateMutation.mutate(
        { id: editId, name, channel_type: channelType, config, enabled },
        {
          onSuccess: () => {
            setOpen(false);
            onClose?.();
          },
        },
      );
    } else {
      createMutation.mutate(
        { name, channel_type: channelType, config, enabled },
        {
          onSuccess: () => {
            setOpen(false);
          },
        },
      );
    }
  };

  const isPending = createMutation.isPending || updateMutation.isPending;

  const dialogContent = (
    <DialogContent className="max-w-lg max-h-[90vh] overflow-y-auto">
      <DialogHeader>
        <DialogTitle>
          {isEdit ? "Edit Channel" : "Create Notification Channel"}
        </DialogTitle>
      </DialogHeader>
      <form onSubmit={handleSubmit} className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor="ch-name">Name</Label>
          <Input
            id="ch-name"
            value={name}
            onChange={(e) => {
              setName(e.target.value);
            }}
            placeholder="Production Alerts"
            required
          />
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <Label>Channel Type</Label>
            <Select
              value={channelType}
              onValueChange={(v) => {
                setChannelType(v as ChannelType);
              }}
              disabled={isEdit}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CHANNEL_TYPES.map((ct) => (
                  <SelectItem key={ct.value} value={ct.value}>
                    {ct.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="flex items-end gap-2 pb-1">
            <Switch checked={enabled} onCheckedChange={setEnabled} />
            <Label>Enabled</Label>
          </div>
        </div>

        {!isEdit && (
          <>
            {channelType === "email" && (
              <EmailFields
                host={smtpHost}
                setHost={setSmtpHost}
                port={smtpPort}
                setPort={setSmtpPort}
                username={smtpUsername}
                setUsername={setSmtpUsername}
                password={smtpPassword}
                setPassword={setSmtpPassword}
                from={smtpFrom}
                setFrom={setSmtpFrom}
                to={smtpTo}
                setTo={setSmtpTo}
                tls={smtpTls}
                setTls={setSmtpTls}
                subjectTemplate={smtpSubjectTemplate}
                setSubjectTemplate={setSmtpSubjectTemplate}
              />
            )}
            {channelType === "slack" && (
              <SlackFields
                webhookUrl={slackWebhookUrl}
                setWebhookUrl={setSlackWebhookUrl}
                channel={slackChannel}
                setChannel={setSlackChannel}
                username={slackUsername}
                setUsername={setSlackUsername}
              />
            )}
            {channelType === "discord" && (
              <DiscordFields
                webhookUrl={discordWebhookUrl}
                setWebhookUrl={setDiscordWebhookUrl}
                username={discordUsername}
                setUsername={setDiscordUsername}
              />
            )}
            {channelType === "teams" && (
              <TeamsFields
                webhookUrl={teamsWebhookUrl}
                setWebhookUrl={setTeamsWebhookUrl}
              />
            )}
            {channelType === "telegram" && (
              <TelegramFields
                botToken={telegramBotToken}
                setBotToken={setTelegramBotToken}
                chatId={telegramChatId}
                setChatId={setTelegramChatId}
              />
            )}
            {channelType === "webhook" && (
              <WebhookFields
                url={webhookUrl}
                setUrl={setWebhookUrl}
                method={webhookMethod}
                setMethod={setWebhookMethod}
                headers={webhookHeaders}
                setHeaders={setWebhookHeaders}
                bodyTemplate={webhookBodyTemplate}
                setBodyTemplate={setWebhookBodyTemplate}
              />
            )}
            {channelType === "pagerduty" && (
              <PagerDutyFields
                routingKey={pdRoutingKey}
                setRoutingKey={setPdRoutingKey}
              />
            )}
            {channelType === "expo_push" && (
              <div className="space-y-2">
                <Label>Recipient user</Label>
                <Select
                  value={expoPushUserId}
                  onValueChange={setExpoPushUserId}
                >
                  <SelectTrigger>
                    <SelectValue placeholder="Choose a user…" />
                  </SelectTrigger>
                  <SelectContent>
                    {(usersQuery.data ?? []).map((u) => (
                      <SelectItem key={u.id} value={u.id}>
                        {u.display_name || u.email} ({u.email})
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <p className="text-xs text-muted-foreground">
                  Notifications fan out to every mobile device this user has
                  registered via the Nexara mobile app.
                </p>
              </div>
            )}
          </>
        )}

        <div className="flex justify-end gap-2 pt-2">
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              setOpen(false);
              onClose?.();
            }}
          >
            Cancel
          </Button>
          <Button type="submit" disabled={isPending}>
            {isPending ? "Saving..." : isEdit ? "Update" : "Create"}
          </Button>
        </div>
      </form>
    </DialogContent>
  );

  if (isEdit) {
    return (
      <Dialog
        open={open}
        onOpenChange={(v) => {
          setOpen(v);
          if (!v) onClose?.();
        }}
      >
        <DialogTrigger asChild>
          <Button variant="outline" size="sm">
            Edit
          </Button>
        </DialogTrigger>
        {dialogContent}
      </Dialog>
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button>
          <Plus className="mr-2 h-4 w-4" />
          Add Channel
        </Button>
      </DialogTrigger>
      {dialogContent}
    </Dialog>
  );
}

// --- Sub-form components ---

function EmailFields({
  host,
  setHost,
  port,
  setPort,
  username,
  setUsername,
  password,
  setPassword,
  from,
  setFrom,
  to,
  setTo,
  tls,
  setTls,
  subjectTemplate,
  setSubjectTemplate,
}: {
  host: string;
  setHost: (v: string) => void;
  port: string;
  setPort: (v: string) => void;
  username: string;
  setUsername: (v: string) => void;
  password: string;
  setPassword: (v: string) => void;
  from: string;
  setFrom: (v: string) => void;
  to: string;
  setTo: (v: string) => void;
  tls: boolean;
  setTls: (v: boolean) => void;
  subjectTemplate: string;
  setSubjectTemplate: (v: string) => void;
}) {
  return (
    <div className="space-y-3 rounded-md border p-3">
      <p className="text-sm font-medium">SMTP Configuration</p>
      <div className="grid grid-cols-3 gap-3">
        <div className="col-span-2 space-y-1">
          <Label>Host</Label>
          <Input
            value={host}
            onChange={(e) => {
              setHost(e.target.value);
            }}
            placeholder="smtp.gmail.com"
            required
          />
        </div>
        <div className="space-y-1">
          <Label>Port</Label>
          <Input
            type="number"
            value={port}
            onChange={(e) => {
              setPort(e.target.value);
            }}
          />
        </div>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1">
          <Label>Username</Label>
          <Input
            value={username}
            onChange={(e) => {
              setUsername(e.target.value);
            }}
          />
        </div>
        <div className="space-y-1">
          <Label>Password</Label>
          <Input
            type="password"
            value={password}
            onChange={(e) => {
              setPassword(e.target.value);
            }}
          />
        </div>
      </div>
      <div className="space-y-1">
        <Label>From</Label>
        <Input
          value={from}
          onChange={(e) => {
            setFrom(e.target.value);
          }}
          placeholder="alerts@example.com"
          required
        />
      </div>
      <div className="space-y-1">
        <Label>To (comma-separated)</Label>
        <Input
          value={to}
          onChange={(e) => {
            setTo(e.target.value);
          }}
          placeholder="admin@example.com, ops@example.com"
          required
        />
      </div>
      <div className="flex items-center gap-2">
        <Switch checked={tls} onCheckedChange={setTls} />
        <Label>Use TLS</Label>
      </div>
      <div className="space-y-1">
        <Label>Subject Template (optional)</Label>
        <Input
          value={subjectTemplate}
          onChange={(e) => {
            setSubjectTemplate(e.target.value);
          }}
          placeholder="[{{.Severity}}] {{.RuleName}} on {{.ResourceName}}"
        />
      </div>
    </div>
  );
}

function SlackFields({
  webhookUrl,
  setWebhookUrl,
  channel,
  setChannel,
  username,
  setUsername,
}: {
  webhookUrl: string;
  setWebhookUrl: (v: string) => void;
  channel: string;
  setChannel: (v: string) => void;
  username: string;
  setUsername: (v: string) => void;
}) {
  return (
    <div className="space-y-3 rounded-md border p-3">
      <p className="text-sm font-medium">Slack Configuration</p>
      <div className="space-y-1">
        <Label>Webhook URL</Label>
        <Input
          value={webhookUrl}
          onChange={(e) => {
            setWebhookUrl(e.target.value);
          }}
          placeholder="https://hooks.slack.com/services/..."
          required
        />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1">
          <Label>Channel (optional)</Label>
          <Input
            value={channel}
            onChange={(e) => {
              setChannel(e.target.value);
            }}
            placeholder="#alerts"
          />
        </div>
        <div className="space-y-1">
          <Label>Username (optional)</Label>
          <Input
            value={username}
            onChange={(e) => {
              setUsername(e.target.value);
            }}
          />
        </div>
      </div>
    </div>
  );
}

function DiscordFields({
  webhookUrl,
  setWebhookUrl,
  username,
  setUsername,
}: {
  webhookUrl: string;
  setWebhookUrl: (v: string) => void;
  username: string;
  setUsername: (v: string) => void;
}) {
  return (
    <div className="space-y-3 rounded-md border p-3">
      <p className="text-sm font-medium">Discord Configuration</p>
      <div className="space-y-1">
        <Label>Webhook URL</Label>
        <Input
          value={webhookUrl}
          onChange={(e) => {
            setWebhookUrl(e.target.value);
          }}
          placeholder="https://discord.com/api/webhooks/..."
          required
        />
      </div>
      <div className="space-y-1">
        <Label>Username (optional)</Label>
        <Input
          value={username}
          onChange={(e) => {
            setUsername(e.target.value);
          }}
        />
      </div>
    </div>
  );
}

function TeamsFields({
  webhookUrl,
  setWebhookUrl,
}: {
  webhookUrl: string;
  setWebhookUrl: (v: string) => void;
}) {
  return (
    <div className="space-y-3 rounded-md border p-3">
      <p className="text-sm font-medium">Microsoft Teams Configuration</p>
      <div className="space-y-1">
        <Label>Webhook URL</Label>
        <Input
          value={webhookUrl}
          onChange={(e) => {
            setWebhookUrl(e.target.value);
          }}
          placeholder="https://outlook.office.com/webhook/..."
          required
        />
      </div>
    </div>
  );
}

function TelegramFields({
  botToken,
  setBotToken,
  chatId,
  setChatId,
}: {
  botToken: string;
  setBotToken: (v: string) => void;
  chatId: string;
  setChatId: (v: string) => void;
}) {
  return (
    <div className="space-y-3 rounded-md border p-3">
      <p className="text-sm font-medium">Telegram Configuration</p>
      <div className="space-y-1">
        <Label>Bot Token</Label>
        <Input
          value={botToken}
          onChange={(e) => {
            setBotToken(e.target.value);
          }}
          placeholder="123456:ABC-DEF..."
          required
        />
      </div>
      <div className="space-y-1">
        <Label>Chat ID</Label>
        <Input
          value={chatId}
          onChange={(e) => {
            setChatId(e.target.value);
          }}
          placeholder="-1001234567890"
          required
        />
      </div>
    </div>
  );
}

function WebhookFields({
  url,
  setUrl,
  method,
  setMethod,
  headers,
  setHeaders,
  bodyTemplate,
  setBodyTemplate,
}: {
  url: string;
  setUrl: (v: string) => void;
  method: string;
  setMethod: (v: string) => void;
  headers: string;
  setHeaders: (v: string) => void;
  bodyTemplate: string;
  setBodyTemplate: (v: string) => void;
}) {
  return (
    <div className="space-y-3 rounded-md border p-3">
      <p className="text-sm font-medium">Webhook Configuration</p>
      <div className="grid grid-cols-3 gap-3">
        <div className="col-span-2 space-y-1">
          <Label>URL</Label>
          <Input
            value={url}
            onChange={(e) => {
              setUrl(e.target.value);
            }}
            placeholder="https://example.com/webhook"
            required
          />
        </div>
        <div className="space-y-1">
          <Label>Method</Label>
          <Select value={method} onValueChange={setMethod}>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="GET">GET</SelectItem>
              <SelectItem value="POST">POST</SelectItem>
              <SelectItem value="PUT">PUT</SelectItem>
              <SelectItem value="PATCH">PATCH</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>
      <div className="space-y-1">
        <Label>Headers (JSON, optional)</Label>
        <Textarea
          value={headers}
          onChange={(e) => {
            setHeaders(e.target.value);
          }}
          placeholder='{"Authorization": "Bearer xxx"}'
          rows={2}
        />
      </div>
      <div className="space-y-1">
        <Label>Body Template (optional)</Label>
        <Textarea
          value={bodyTemplate}
          onChange={(e) => {
            setBodyTemplate(e.target.value);
          }}
          placeholder='{"alert": "{{.RuleName}}", "severity": "{{.Severity}}"}'
          rows={3}
        />
        <p className="text-xs text-muted-foreground">
          Leave empty to send full alert payload as JSON
        </p>
      </div>
    </div>
  );
}

function PagerDutyFields({
  routingKey,
  setRoutingKey,
}: {
  routingKey: string;
  setRoutingKey: (v: string) => void;
}) {
  return (
    <div className="space-y-3 rounded-md border p-3">
      <p className="text-sm font-medium">PagerDuty Configuration</p>
      <div className="space-y-1">
        <Label>Integration/Routing Key</Label>
        <Input
          value={routingKey}
          onChange={(e) => {
            setRoutingKey(e.target.value);
          }}
          placeholder="service-integration-key"
          required
        />
      </div>
    </div>
  );
}
