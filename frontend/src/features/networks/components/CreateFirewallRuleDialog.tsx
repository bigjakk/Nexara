import { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Plus } from "lucide-react";
import { useCreateClusterFirewallRule } from "../api/network-queries";
import type { FirewallRuleRequest } from "../types/network";

interface CreateFirewallRuleDialogProps {
  clusterId: string;
}

export function CreateFirewallRuleDialog({
  clusterId,
}: CreateFirewallRuleDialogProps) {
  const [open, setOpen] = useState(false);
  const [type, setType] = useState("in");
  const [action, setAction] = useState("ACCEPT");
  const [proto, setProto] = useState("");
  const [source, setSource] = useState("");
  const [dest, setDest] = useState("");
  const [dport, setDport] = useState("");
  const [comment, setComment] = useState("");
  const [enable, setEnable] = useState(true);

  const create = useCreateClusterFirewallRule(clusterId);

  const handleSubmit = () => {
    const req: FirewallRuleRequest = {
      type,
      action,
      enable: enable ? 1 : 0,
    };
    if (proto) req.proto = proto;
    if (source) req.source = source;
    if (dest) req.dest = dest;
    if (dport) req.dport = dport;
    if (comment) req.comment = comment;
    create.mutate(
      req,
      {
        onSuccess: () => {
          setOpen(false);
          setProto("");
          setSource("");
          setDest("");
          setDport("");
          setComment("");
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus className="mr-1 h-4 w-4" />
          Add Rule
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create Firewall Rule</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>Direction</Label>
              <Select value={type} onValueChange={setType}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="in">IN</SelectItem>
                  <SelectItem value="out">OUT</SelectItem>
                  <SelectItem value="group">GROUP</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label>Action</Label>
              <Select value={action} onValueChange={setAction}>
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
          <div className="space-y-2">
            <Label>Protocol (optional)</Label>
            <Select value={proto} onValueChange={setProto}>
              <SelectTrigger>
                <SelectValue placeholder="Any" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="tcp">TCP</SelectItem>
                <SelectItem value="udp">UDP</SelectItem>
                <SelectItem value="icmp">ICMP</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>Source (optional)</Label>
              <Input
                placeholder="e.g. 10.0.0.0/24"
                value={source}
                onChange={(e) => { setSource(e.target.value); }}
              />
            </div>
            <div className="space-y-2">
              <Label>Destination (optional)</Label>
              <Input
                placeholder="e.g. 10.0.1.0/24"
                value={dest}
                onChange={(e) => { setDest(e.target.value); }}
              />
            </div>
          </div>
          <div className="space-y-2">
            <Label>Destination Port (optional)</Label>
            <Input
              placeholder="e.g. 80, 443, 8000-9000"
              value={dport}
              onChange={(e) => { setDport(e.target.value); }}
            />
          </div>
          <div className="space-y-2">
            <Label>Comment (optional)</Label>
            <Input
              placeholder="Rule description"
              value={comment}
              onChange={(e) => { setComment(e.target.value); }}
            />
          </div>
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="enable-rule"
              checked={enable}
              onChange={(e) => { setEnable(e.target.checked); }}
              className="rounded border"
            />
            <Label htmlFor="enable-rule">Enable rule</Label>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => { setOpen(false); }}>
              Cancel
            </Button>
            <Button onClick={handleSubmit} disabled={create.isPending}>
              Create
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
