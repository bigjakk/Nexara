import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { usePermissions } from "@/hooks/usePermissions";
import { useSaveOutcome } from "@/hooks/useSaveOutcome";
import { ApiClientError } from "@/lib/api-client";
import type { HealthIssue } from "@/types/api";
import { useAcceptClusterName } from "../api/cluster-queries";
import { clusterNameChange } from "../lib/cluster-name-change";

/**
 * "Accept new name" beside a cluster_name_changed issue, for someone with
 * manage:cluster: the one way to accept the name short of the API. It asks
 * first, naming both clusters and what accepting does to the old one's nodes,
 * and sends the reported name exactly as shown, so a cluster that answers
 * differently since is refused (a 409, shown in the dialog).
 */
export function AcceptClusterName({
  clusterId,
  issue,
}: {
  clusterId: string;
  issue: HealthIssue;
}) {
  const { t } = useTranslation("clusters");
  const { hasPermission } = usePermissions();
  const names = clusterNameChange(issue);
  const [open, setOpen] = useState(false);
  const [error, setError] = useState("");
  const accept = useAcceptClusterName(clusterId);
  const settle = useSaveOutcome(open);

  if (names === null || !hasPermission("manage", "cluster")) return null;

  return (
    <>
      <Button
        size="sm"
        variant="outline"
        className="mt-2"
        onClick={() => {
          setError("");
          setOpen(true);
        }}
      >
        {t("acceptNewName")}
      </Button>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (!next && accept.isPending) return;
          setOpen(next);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {t("acceptNewNameTitle", { reported: names.reported })}
            </DialogTitle>
            <DialogDescription>
              {t("acceptNewNameDescription", names)}
            </DialogDescription>
          </DialogHeader>
          {error !== "" && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
          <DialogFooter>
            <Button
              variant="outline"
              disabled={accept.isPending}
              onClick={() => {
                setOpen(false);
              }}
            >
              {t("cancel", { ns: "common" })}
            </Button>
            <Button
              variant="destructive"
              disabled={accept.isPending}
              onClick={() => {
                setError("");
                settle(accept.mutateAsync(names.reported), {
                  action: t("acceptNewNameAction", names),
                  onSuccess: () => {
                    setOpen(false);
                  },
                  onError: (err) => {
                    setError(
                      err instanceof ApiClientError
                        ? err.message
                        : t("acceptNewNameFailed"),
                    );
                  },
                });
              }}
            >
              {t("acceptNewName")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
