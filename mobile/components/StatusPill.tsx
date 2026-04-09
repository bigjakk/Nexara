import { Text, View } from "react-native";

export type StatusTone =
  | "success"
  | "warning"
  | "danger"
  | "neutral"
  | "info";
type Tone = StatusTone;

const TONE_CLASSES: Record<Tone, string> = {
  success: "bg-primary/20 text-primary",
  warning: "bg-yellow-500/20 text-yellow-400",
  danger: "bg-destructive/20 text-destructive",
  info: "bg-blue-500/20 text-blue-400",
  neutral: "bg-muted text-muted-foreground",
};

/**
 * Compact rounded badge for resource status (online, running, firing, etc.).
 * Tone is a semantic color hint; pick the closest match for the status.
 */
export function StatusPill({
  label,
  tone = "neutral",
}: {
  label: string;
  tone?: Tone;
}) {
  const classes = TONE_CLASSES[tone];
  return (
    <View className={`rounded-full px-2 py-0.5 ${classes.split(" ")[0] ?? ""}`}>
      <Text
        className={`text-[10px] font-medium uppercase ${classes.split(" ")[1] ?? ""}`}
      >
        {label}
      </Text>
    </View>
  );
}

export function statusToneFor(status: string): Tone {
  switch (status.toLowerCase()) {
    case "online":
    case "running":
    case "resolved":
      return "success";
    case "degraded":
    case "warning":
    case "paused":
    case "suspended":
    case "acknowledged":
      return "warning";
    case "offline":
    case "stopped":
    case "critical":
    case "firing":
      return "danger";
    case "pending":
    case "info":
      return "info";
    default:
      return "neutral";
  }
}
