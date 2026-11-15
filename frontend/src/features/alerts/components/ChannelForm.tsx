import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
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
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Plus, ShieldAlert } from "lucide-react";
import {
  useCreateNotificationChannel,
  useUpdateNotificationChannel,
} from "../api/alert-queries";
import type { ChannelType } from "@/types/api";
import { parseWebhookHeaders } from "../lib/webhook-headers";
import { PrivateAddressWarning } from "@/components/PrivateAddressWarning";
import { privateAddressWarningFromError } from "@/lib/private-address";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { Field, FormSection } from "./FormParts";

const CHANNEL_TYPES: { value: ChannelType; label: string }[] = [
  { value: "email", label: "Email (SMTP)" },
  { value: "slack", label: "Slack" },
  { value: "discord", label: "Discord" },
  { value: "teams", label: "Microsoft Teams" },
  { value: "telegram", label: "Telegram" },
  { value: "webhook", label: "Generic webhook" },
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
  const { t } = useTranslation("alerts");
  const isEdit = !!editId;
  const [open, setOpen] = useState(false);
  const [name, setName] = useState(editName ?? "");
  const [channelType, setChannelType] = useState<ChannelType>(
    editType ?? "email",
  );
  const [enabled, setEnabled] = useState(editEnabled ?? true);
  const enabledId = useId();

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

  const createMutation = useCreateNotificationChannel();
  const updateMutation = useUpdateNotificationChannel();
  // The add dialog stays mounted and toggles, so its open flag tells one
  // dialog's save from the next's.
  const settle = useSaveOutcome(open);
  // A target on a private network is refused until confirmed (422); the
  // confirmation is stored on the channel.
  const privateWarning = isEdit
    ? null
    : privateAddressWarningFromError(createMutation.error);

  // Invalid headers hold Save: sent as {} they would store a channel that
  // silently drops the Authorization the operator typed.
  const parsedHeaders = parseWebhookHeaders(webhookHeaders);
  const headersError =
    !isEdit && channelType === "webhook" && parsedHeaders.error !== null
      ? t(parsedHeaders.error === "json" ? "headersNotJSON" : "headersNotStrings")
      : null;

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
      case "webhook":
        return {
          url: webhookUrl,
          method: webhookMethod,
          headers: parsedHeaders.headers ?? {},
          body_template: webhookBodyTemplate || undefined,
        };
      case "pagerduty":
        return { routing_key: pdRoutingKey };
      default:
        return {};
    }
  };

  const handleSubmit = (e: React.SyntheticEvent) => {
    e.preventDefault();
    if (headersError !== null) return;

    if (isEdit && editId) {
      // Edit shows no settings fields (the stored ones are never sent back),
      // so it sends no config: the server replaces the stored settings with
      // any config it is given, and blank fields would stop the channel.
      updateMutation.mutate(
        { id: editId, name, enabled },
        {
          onSuccess: () => {
            setOpen(false);
            onClose?.();
          },
        },
      );
    } else {
      create(false);
    }
  };

  const create = (allowPrivate: boolean) => {
    settle(
      createMutation.mutateAsync({
        name,
        channel_type: channelType,
        config: buildConfig(),
        enabled,
        ...(allowPrivate ? { allow_private_address: true } : {}),
      }),
      {
        // Named: a late failure is toasted, perhaps over another page.
        action: `Adding notification channel ${name}`,
        onSuccess: () => {
          setOpen(false);
        },
        // Shown below from the mutation's own state: the private-address
        // prompt, or the error itself.
        onError: () => undefined,
      },
    );
  };

  const isPending = createMutation.isPending || updateMutation.isPending;

  const dialogContent = (
    <DialogContent className="max-w-lg">
      <DialogHeader>
        <DialogTitle>
          {isEdit ? "Edit channel" : "Add notification channel"}
        </DialogTitle>
        <DialogDescription>
          {isEdit
            ? "Rename the channel or turn it off. Its stored settings stay as they are."
            : "Where alert notifications are sent. Its settings are stored encrypted and never shown again."}
        </DialogDescription>
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
            placeholder="Production alerts"
            required
          />
        </div>

        <div className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-4">
          <Field label="Channel type">
            {(id) => (
              <Select
                value={channelType}
                onValueChange={(v) => {
                  setChannelType(v as ChannelType);
                }}
                disabled={isEdit}
              >
                <SelectTrigger id={id}>
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
            )}
          </Field>

          <div className="flex h-9 items-center gap-2">
            <Switch
              id={enabledId}
              checked={enabled}
              onCheckedChange={setEnabled}
            />
            <Label htmlFor={enabledId}>Enabled</Label>
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
                headersError={headersError}
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
          </>
        )}

        {privateWarning !== null && (
          <PrivateAddressWarning
            ip={privateWarning.ip}
            pending={createMutation.isPending}
            onConfirm={() => {
              create(true);
            }}
            onCancel={() => {
              createMutation.reset();
            }}
          />
        )}
        {!isEdit && createMutation.error !== null && privateWarning === null && (
          <p role="alert" className="text-sm text-destructive">
            {createMutation.error.message}
          </p>
        )}

        <DialogFooter className="gap-2 sm:space-x-0">
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              setOpen(false);
              createMutation.reset();
              onClose?.();
            }}
          >
            Cancel
          </Button>
          <Button type="submit" disabled={isPending || headersError !== null}>
            {isPending ? "Saving..." : isEdit ? "Update" : "Create"}
          </Button>
        </DialogFooter>
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
    <Dialog
      open={open}
      onOpenChange={(v) => {
        setOpen(v);
        if (!v) createMutation.reset();
      }}
    >
      <DialogTrigger asChild>
        <Button variant="outline" className="gap-1.5">
          <Plus className="h-4 w-4" />
          Add channel
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
  const tlsId = useId();
  return (
    <FormSection title="SMTP settings">
      <div className="grid grid-cols-3 gap-3">
        <Field label="Host" className="col-span-2">
          {(id) => (
            <Input
              id={id}
              value={host}
              onChange={(e) => {
                setHost(e.target.value);
              }}
              placeholder="smtp.example.com"
              required
            />
          )}
        </Field>
        <Field label="Port">
          {(id) => (
            <Input
              id={id}
              type="number"
              value={port}
              onChange={(e) => {
                setPort(e.target.value);
              }}
            />
          )}
        </Field>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Username">
          {(id) => (
            <Input
              id={id}
              value={username}
              onChange={(e) => {
                setUsername(e.target.value);
              }}
            />
          )}
        </Field>
        <Field label="Password">
          {(id) => (
            <Input
              id={id}
              type="password"
              value={password}
              onChange={(e) => {
                setPassword(e.target.value);
              }}
            />
          )}
        </Field>
      </div>
      <Field label="From">
        {(id) => (
          <Input
            id={id}
            value={from}
            onChange={(e) => {
              setFrom(e.target.value);
            }}
            placeholder="alerts@example.com"
            required
          />
        )}
      </Field>
      <Field label="To (comma-separated)">
        {(id) => (
          <Input
            id={id}
            value={to}
            onChange={(e) => {
              setTo(e.target.value);
            }}
            placeholder="admin@example.com, ops@example.com"
            required
          />
        )}
      </Field>
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <Switch id={tlsId} checked={tls} onCheckedChange={setTls} />
          <Label htmlFor={tlsId}>Use STARTTLS</Label>
        </div>
        {!tls && (
          <div className="flex items-start gap-2 rounded-md bg-amber-500/10 p-3 text-xs text-amber-700 dark:text-amber-400">
            <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0" />
            <div className="space-y-1">
              <p className="font-medium">
                Cleartext SMTP: local development only
              </p>
              <p>
                With STARTTLS off, credentials and message bodies are sent
                unencrypted. The server will refuse cleartext sends to any host
                that does not resolve to a loopback address (e.g.{" "}
                <code>127.0.0.1</code>, <code>localhost</code>). Leave this on
                for any production relay.
              </p>
            </div>
          </div>
        )}
      </div>
      <Field label="Subject template (optional)">
        {(id) => (
          <Input
            id={id}
            value={subjectTemplate}
            onChange={(e) => {
              setSubjectTemplate(e.target.value);
            }}
            placeholder="[{{.Severity}}] {{.RuleName}} on {{.ResourceName}}"
          />
        )}
      </Field>
    </FormSection>
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
    <FormSection title="Slack settings">
      <Field label="Webhook URL">
        {(id) => (
          <Input
            id={id}
            value={webhookUrl}
            onChange={(e) => {
              setWebhookUrl(e.target.value);
            }}
            placeholder="https://hooks.slack.com/services/..."
            required
          />
        )}
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field label="Channel (optional)">
          {(id) => (
            <Input
              id={id}
              value={channel}
              onChange={(e) => {
                setChannel(e.target.value);
              }}
              placeholder="#alerts"
            />
          )}
        </Field>
        <Field label="Username (optional)">
          {(id) => (
            <Input
              id={id}
              value={username}
              onChange={(e) => {
                setUsername(e.target.value);
              }}
            />
          )}
        </Field>
      </div>
    </FormSection>
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
    <FormSection title="Discord settings">
      <Field label="Webhook URL">
        {(id) => (
          <Input
            id={id}
            value={webhookUrl}
            onChange={(e) => {
              setWebhookUrl(e.target.value);
            }}
            placeholder="https://discord.com/api/webhooks/..."
            required
          />
        )}
      </Field>
      <Field label="Username (optional)">
        {(id) => (
          <Input
            id={id}
            value={username}
            onChange={(e) => {
              setUsername(e.target.value);
            }}
          />
        )}
      </Field>
    </FormSection>
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
    <FormSection title="Microsoft Teams settings">
      <Field label="Webhook URL">
        {(id) => (
          <Input
            id={id}
            value={webhookUrl}
            onChange={(e) => {
              setWebhookUrl(e.target.value);
            }}
            placeholder="https://outlook.office.com/webhook/..."
            required
          />
        )}
      </Field>
    </FormSection>
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
    <FormSection title="Telegram settings">
      <Field label="Bot token">
        {(id) => (
          <Input
            id={id}
            value={botToken}
            onChange={(e) => {
              setBotToken(e.target.value);
            }}
            placeholder="123456:ABC-DEF..."
            required
          />
        )}
      </Field>
      <Field label="Chat ID">
        {(id) => (
          <Input
            id={id}
            value={chatId}
            onChange={(e) => {
              setChatId(e.target.value);
            }}
            placeholder="-1001234567890"
            required
          />
        )}
      </Field>
    </FormSection>
  );
}

function WebhookFields({
  url,
  setUrl,
  method,
  setMethod,
  headers,
  setHeaders,
  headersError,
  bodyTemplate,
  setBodyTemplate,
}: {
  url: string;
  setUrl: (v: string) => void;
  method: string;
  setMethod: (v: string) => void;
  headers: string;
  setHeaders: (v: string) => void;
  headersError: string | null;
  bodyTemplate: string;
  setBodyTemplate: (v: string) => void;
}) {
  return (
    <FormSection title="Webhook settings">
      <div className="grid grid-cols-3 gap-3">
        <Field label="URL" className="col-span-2">
          {(id) => (
            <Input
              id={id}
              value={url}
              onChange={(e) => {
                setUrl(e.target.value);
              }}
              placeholder="https://example.com/webhook"
              required
            />
          )}
        </Field>
        <Field label="Method">
          {(id) => (
            <Select value={method} onValueChange={setMethod}>
              <SelectTrigger id={id}>
                <SelectValue />
              </SelectTrigger>
              {/* No GET: a send refuses it (webhook.go). */}
              <SelectContent>
                <SelectItem value="POST">POST</SelectItem>
                <SelectItem value="PUT">PUT</SelectItem>
                <SelectItem value="PATCH">PATCH</SelectItem>
              </SelectContent>
            </Select>
          )}
        </Field>
      </div>
      <Field label="Headers (JSON, optional)" error={headersError}>
        {(id) => (
          <Textarea
            id={id}
            value={headers}
            onChange={(e) => {
              setHeaders(e.target.value);
            }}
            placeholder='{"Authorization": "Bearer xxx"}'
            rows={2}
            aria-invalid={headersError !== null}
          />
        )}
      </Field>
      <Field
        label="Body template (optional)"
        hint="Leave empty to send full alert payload as JSON"
      >
        {(id) => (
          <Textarea
            id={id}
            value={bodyTemplate}
            onChange={(e) => {
              setBodyTemplate(e.target.value);
            }}
            placeholder='{"alert": "{{.RuleName}}", "severity": "{{.Severity}}"}'
            rows={3}
          />
        )}
      </Field>
    </FormSection>
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
    <FormSection title="PagerDuty settings">
      <Field label="Integration or routing key">
        {(id) => (
          <Input
            id={id}
            value={routingKey}
            onChange={(e) => {
              setRoutingKey(e.target.value);
            }}
            placeholder="service-integration-key"
            required
          />
        )}
      </Field>
    </FormSection>
  );
}
