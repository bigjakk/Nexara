import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Plus } from "lucide-react";
import { InterfaceFormDialog } from "./InterfaceFormDialog";

interface CreateInterfaceDialogProps {
  clusterId: string;
  nodeName: string;
}

/** The Create Interface button and the form it opens. */
export function CreateInterfaceDialog({
  clusterId,
  nodeName,
}: CreateInterfaceDialogProps) {
  const [open, setOpen] = useState(false);

  return (
    <>
      <Button
        size="sm"
        onClick={() => {
          setOpen(true);
        }}
      >
        <Plus className="mr-1 h-4 w-4" />
        Create Interface
      </Button>
      <InterfaceFormDialog
        clusterId={clusterId}
        nodeName={nodeName}
        open={open}
        onOpenChange={setOpen}
      />
    </>
  );
}
