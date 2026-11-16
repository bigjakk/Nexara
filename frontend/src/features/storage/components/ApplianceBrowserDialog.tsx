import { useMemo, useState, useEffect } from "react";
import { LibraryBig, RefreshCw, Search } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Segmented } from "@/components/ui/segmented";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { TaskProgressBanner } from "@/features/vms/components/TaskProgressBanner";
import { useAppliances, useDownloadAppliance } from "../api/storage-queries";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import type { ApplianceTemplate } from "../types/storage";
import { useTranslation } from "react-i18next";

interface ApplianceBrowserDialogProps {
  clusterId: string;
  storageId: string;
  storageName: string;
  /**
   * With `open` the dialog is controlled and draws no trigger of its own: the
   * caller opens it, from a menu item.
   */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}

export function ApplianceBrowserDialog({
  clusterId,
  storageId,
  storageName,
  open: openProp,
  onOpenChange,
}: ApplianceBrowserDialogProps) {
  const { t: ts } = useTranslation("storage");
  const [internalOpen, setInternalOpen] = useState(false);
  const controlled = openProp !== undefined;
  const open = controlled ? openProp : internalOpen;
  const setOpen = (v: boolean) => {
    if (!controlled) setInternalOpen(v);
    onOpenChange?.(v);
  };
  const [search, setSearch] = useState("");
  const [section, setSection] = useState<string>("all");
  const [taskUpid, setTaskUpid] = useState<string | null>(null);
  const [activeTemplate, setActiveTemplate] = useState<string>("");

  const appliancesQuery = useAppliances(clusterId, open);
  const downloadMutation = useDownloadAppliance();
  const settle = useSaveOutcome(open);
  const queryClient = useQueryClient();

  const appliances: ApplianceTemplate[] = useMemo(
    () => appliancesQuery.data ?? [],
    [appliancesQuery.data],
  );

  const sections = useMemo(() => {
    const seen = new Set<string>();
    for (const a of appliances) {
      if (a.section) seen.add(a.section);
    }
    return ["all", ...Array.from(seen).sort()];
  }, [appliances]);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return appliances.filter((a) => {
      if (section !== "all" && a.section !== section) return false;
      if (q.length === 0) return true;
      return (
        a.template.toLowerCase().includes(q) ||
        a.os.toLowerCase().includes(q) ||
        a.description.toLowerCase().includes(q) ||
        a.headline.toLowerCase().includes(q)
      );
    });
  }, [appliances, search, section]);

  function handleDownload(t: ApplianceTemplate) {
    setActiveTemplate(t.template);
    setTaskUpid(null);
    settle(
      downloadMutation.mutateAsync({
        clusterId,
        storageId,
        data: { template: t.template },
      }),
      {
        action: ts(
          "applianceBrowserDialogActions.downloadingTemplateToStorageName",
          { template: t.template, storageName },
        ),
        onSuccess: (data) => {
          if (data.upid) setTaskUpid(data.upid);
        },
        onError: () => undefined, // shown below from the mutation's state
      },
    );
  }

  useEffect(() => {
    if (!open) {
      setSearch("");
      setSection("all");
      setTaskUpid(null);
      setActiveTemplate("");
      downloadMutation.reset();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const isBusy = downloadMutation.isPending || taskUpid !== null;

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v && isBusy) return;
        setOpen(v);
      }}
    >
      {!controlled && (
        <DialogTrigger asChild>
          <Button size="sm" variant="outline">
            <LibraryBig className="mr-2 h-4 w-4" />
            Browse appliances
          </Button>
        </DialogTrigger>
      )}
      <DialogContent className="max-h-[90vh] max-w-4xl overflow-hidden">
        <DialogHeader>
          <DialogTitle>Proxmox appliance catalog</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-3 overflow-hidden">
          <p className="text-[12.5px] text-muted-foreground">
            Official templates curated by Proxmox. Downloads go to{" "}
            <span className="font-mono">{storageName}</span>.
          </p>

          <div className="flex items-center gap-2">
            <div className="relative flex-1">
              <Search className="absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                placeholder="Search by name, OS, or description…"
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value);
                }}
                className="pl-8"
                disabled={isBusy}
              />
            </div>
            <Button
              aria-label="Refresh appliance list"
              size="sm"
              variant="outline"
              onClick={() => {
                void appliancesQuery.refetch();
              }}
              disabled={appliancesQuery.isFetching}
            >
              <RefreshCw
                className={`h-4 w-4 ${appliancesQuery.isFetching ? "animate-spin" : ""}`}
              />
            </Button>
          </div>

          {sections.length > 1 && (
            <div>
              <Segmented
                aria-label="Section"
                value={section}
                onValueChange={setSection}
                options={sections.map((s) => ({
                  value: s,
                  label: s === "all" ? "All" : s,
                  disabled: isBusy,
                }))}
              />
            </div>
          )}

          {taskUpid && (
            <TaskProgressBanner
              clusterId={clusterId}
              upid={taskUpid}
              description={`Download ${activeTemplate}`}
              onComplete={() => {
                void queryClient.invalidateQueries({
                  queryKey: [
                    "clusters",
                    clusterId,
                    "storage",
                    storageId,
                    "content",
                  ],
                });
                setTaskUpid(null);
                setActiveTemplate("");
              }}
            />
          )}

          {/* The table lines up with the dialog's own padding, not a panel's
              gutters. */}
          <div
            className="overflow-y-auto border-t [&_td:first-child]:pl-0 [&_td:last-child]:pr-0 [&_th:first-child]:pl-0 [&_th:last-child]:pr-0"
            style={{ maxHeight: "55vh" }}
          >
            {appliancesQuery.isLoading ? (
              <div className="space-y-1 py-2">
                <Skeleton className="h-8 w-full" />
                <Skeleton className="h-8 w-full" />
                <Skeleton className="h-8 w-full" />
                <Skeleton className="h-8 w-full" />
              </div>
            ) : appliancesQuery.isError ? (
              <p className="py-4 text-sm text-destructive">
                Failed to load appliance catalog.
              </p>
            ) : filtered.length === 0 ? (
              <p className="p-4 text-center text-sm text-muted-foreground">
                No matching appliances.
              </p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>OS</TableHead>
                    <TableHead>Version</TableHead>
                    <TableHead>Section</TableHead>
                    <TableHead>Description</TableHead>
                    <TableHead className="text-right">
                      <span className="sr-only">Download</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filtered.map((a) => (
                    <TableRow key={a.template}>
                      <TableCell className="font-medium">{a.os}</TableCell>
                      <TableCell className="font-mono text-[12.5px]">
                        {a.version}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {a.section}
                      </TableCell>
                      <TableCell className="max-w-md">
                        <p
                          className="truncate text-[12.5px] text-muted-foreground"
                          title={a.description || a.headline}
                        >
                          {a.headline || a.description || a.template}
                        </p>
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => {
                            handleDownload(a);
                          }}
                          disabled={isBusy}
                        >
                          {downloadMutation.isPending &&
                          activeTemplate === a.template
                            ? "…"
                            : "Download"}
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>

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
