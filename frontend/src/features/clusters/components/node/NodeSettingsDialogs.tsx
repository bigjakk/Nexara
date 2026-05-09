import { useState } from "react";
import { Pencil } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  useNodeDNS,
  useSetNodeDNS,
  useSetNodeTimezone,
} from "../../api/cluster-queries";

export function EditDNSDialog({ clusterId, nodeName }: { clusterId: string; nodeName: string }) {
  const [open, setOpen] = useState(false);
  const { data: dns } = useNodeDNS(clusterId, nodeName);
  const [search, setSearch] = useState("");
  const [dns1, setDns1] = useState("");
  const [dns2, setDns2] = useState("");
  const [dns3, setDns3] = useState("");
  const setNodeDNS = useSetNodeDNS(clusterId, nodeName);

  const handleOpen = (isOpen: boolean) => {
    if (isOpen && dns) {
      setSearch(dns.search);
      setDns1(dns.dns1);
      setDns2(dns.dns2);
      setDns3(dns.dns3);
    }
    setOpen(isOpen);
  };

  const handleSave = () => {
    setNodeDNS.mutate(
      { search, dns1, dns2, dns3 },
      { onSuccess: () => { setOpen(false); } },
    );
  };

  return (
    <Dialog open={open} onOpenChange={handleOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon" className="h-6 w-6">
          <Pencil className="h-3 w-3" />
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit DNS Configuration - {nodeName}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>Search Domain</Label>
            <Input value={search} onChange={(e) => { setSearch(e.target.value); }} placeholder="e.g. example.com" />
          </div>
          <div className="space-y-2">
            <Label>DNS Server 1</Label>
            <Input value={dns1} onChange={(e) => { setDns1(e.target.value); }} placeholder="e.g. 8.8.8.8" />
          </div>
          <div className="space-y-2">
            <Label>DNS Server 2</Label>
            <Input value={dns2} onChange={(e) => { setDns2(e.target.value); }} placeholder="e.g. 8.8.4.4" />
          </div>
          <div className="space-y-2">
            <Label>DNS Server 3</Label>
            <Input value={dns3} onChange={(e) => { setDns3(e.target.value); }} placeholder="Optional" />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => { setOpen(false); }}>Cancel</Button>
            <Button onClick={handleSave} disabled={!search || setNodeDNS.isPending}>Save</Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}

export function EditTimezoneDialog({ clusterId, nodeName, currentTimezone }: { clusterId: string; nodeName: string; currentTimezone: string }) {
  const [open, setOpen] = useState(false);
  const [timezone, setTimezone] = useState(currentTimezone || "UTC");
  const setNodeTimezone = useSetNodeTimezone(clusterId, nodeName);

  const handleOpen = (isOpen: boolean) => {
    if (isOpen) {
      setTimezone(currentTimezone || "UTC");
    }
    setOpen(isOpen);
  };

  const handleSave = () => {
    setNodeTimezone.mutate(
      { timezone },
      { onSuccess: () => { setOpen(false); } },
    );
  };

  return (
    <Dialog open={open} onOpenChange={handleOpen}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="icon" className="h-6 w-6">
          <Pencil className="h-3 w-3" />
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit Timezone - {nodeName}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>Timezone</Label>
            <Input value={timezone} onChange={(e) => { setTimezone(e.target.value); }} placeholder="e.g. America/New_York" />
            <p className="text-xs text-muted-foreground">Enter an IANA timezone (e.g. UTC, America/New_York, Europe/London)</p>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => { setOpen(false); }}>Cancel</Button>
            <Button onClick={handleSave} disabled={!timezone || setNodeTimezone.isPending}>Save</Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
