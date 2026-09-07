import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { QueryStateNotice } from "@/components/QueryStateNotice";
import {
  useFirewallOptions,
  useSetFirewallOptions,
} from "../api/network-queries";

interface FirewallOptionsCardProps {
  clusterId: string;
}

export function FirewallOptionsCard({ clusterId }: FirewallOptionsCardProps) {
  const optionsQuery = useFirewallOptions(clusterId);
  const opts = optionsQuery.data;
  const setOpts = useSetFirewallOptions(clusterId);

  // `opts?.enable === 1` is false for a read that failed as readily as for a
  // firewall that is off, and this badge is a statement about the cluster's
  // security posture. Say nothing rather than say "Disabled" about a setting
  // nobody managed to read — and withhold the controls, which would otherwise
  // offer to toggle from a state that was guessed.
  if (!opts) {
    return (
      <div className="space-y-4">
        <Card>
          <CardHeader>
            <CardTitle>Cluster Firewall</CardTitle>
          </CardHeader>
          <CardContent>
            <QueryStateNotice
              query={optionsQuery}
              subject="the cluster firewall options"
              empty="Proxmox returned no firewall options for this cluster."
              skeletonClassName="h-20 w-full"
            />
          </CardContent>
        </Card>
      </div>
    );
  }

  const isEnabled = opts.enable === 1;

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-between">
            <span>Cluster Firewall</span>
            <Badge variant={isEnabled ? "default" : "secondary"}>
              {isEnabled ? "Enabled" : "Disabled"}
            </Badge>
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex items-center gap-4">
            <Button
              variant={isEnabled ? "destructive" : "default"}
              size="sm"
              onClick={() => {
                setOpts.mutate({ enable: isEnabled ? 0 : 1 });
              }}
              disabled={setOpts.isPending}
            >
              {isEnabled ? "Disable Firewall" : "Enable Firewall"}
            </Button>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>Inbound Policy</Label>
              <Select
                value={opts.policy_in || "DROP"}
                onValueChange={(val) => {
                  setOpts.mutate({ policy_in: val });
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="ACCEPT">ACCEPT</SelectItem>
                  <SelectItem value="DROP">DROP</SelectItem>
                  <SelectItem value="REJECT">REJECT</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label>Outbound Policy</Label>
              <Select
                value={opts.policy_out || "ACCEPT"}
                onValueChange={(val) => {
                  setOpts.mutate({ policy_out: val });
                }}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="ACCEPT">ACCEPT</SelectItem>
                  <SelectItem value="DROP">DROP</SelectItem>
                  <SelectItem value="REJECT">REJECT</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
