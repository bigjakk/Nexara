import { Badge } from "@/components/ui/badge";

interface CephHealthBadgeProps {
  status: string;
}

export function CephHealthBadge({ status }: CephHealthBadgeProps) {
  const variant = getHealthVariant(status);
  const label = getHealthLabel(status);

  return <Badge variant={variant}>{label}</Badge>;
}

function getHealthVariant(
  status: string,
): "default" | "secondary" | "destructive" | "outline" {
  switch (status) {
    case "HEALTH_OK":
      return "default";
    case "HEALTH_WARN":
      return "secondary";
    case "HEALTH_ERR":
      return "destructive";
    default:
      return "outline";
  }
}

function getHealthLabel(status: string): string {
  switch (status) {
    case "HEALTH_OK":
      return "Healthy";
    case "HEALTH_WARN":
      return "Warning";
    case "HEALTH_ERR":
      return "Error";
    default:
      return status || "Unknown";
  }
}
