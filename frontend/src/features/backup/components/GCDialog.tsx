import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Trash2 } from "lucide-react";
import { useTriggerGC } from "../api/backup-queries";

interface GCDialogProps {
  pbsId: string;
  store: string;
}

export function GCDialog({ pbsId, store }: GCDialogProps) {
  const { t } = useTranslation("backup");
  const [open, setOpen] = useState(false);
  const gcMutation = useTriggerGC();

  const handleConfirm = () => {
    gcMutation.mutate(
      { pbsId, store },
      {
        onSuccess: () => {
          setOpen(false);
        },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm" className="gap-1.5">
          <Trash2 className="h-3.5 w-3.5" />
          {t("runGC")}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("gcDialog.title")}</DialogTitle>
          <DialogDescription>
            <Trans
              t={t}
              i18nKey="gcDialog.description"
              values={{ store }}
              components={{ strong: <strong /> }}
            />
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              setOpen(false);
            }}
          >
            {t("common:cancel")}
          </Button>
          <Button onClick={handleConfirm} disabled={gcMutation.isPending}>
            {gcMutation.isPending ? t("starting") : t("runGC")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
