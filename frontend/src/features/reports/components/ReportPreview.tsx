import { useEffect, useRef, useState } from "react";
import { Printer } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useReportRunHTML } from "../api/report-queries";

interface ReportPreviewProps {
  runId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function ReportPreview({
  runId,
  open,
  onOpenChange,
}: ReportPreviewProps) {
  const { data: html, isLoading } = useReportRunHTML(open ? runId : "");
  const frameRef = useRef<HTMLIFrameElement>(null);
  const [frameReady, setFrameReady] = useState(false);

  // srcDoc swaps in place when the run changes, and a stale `true` here would
  // let Print fire against the previous report's document.
  useEffect(() => {
    setFrameReady(false);
  }, [runId, html]);

  /**
   * Hand the report to the browser's print dialog, which is also its
   * "Save as PDF" path.
   *
   * Printing the iframe rather than the page means the print stylesheet in
   * internal/reports/renderer.go applies to the report alone — no dialog
   * chrome, no app shell. The sandbox below must carry allow-modals for this:
   * print() counts as a modal, and without the flag the call is swallowed with
   * no error, which looks exactly like a dead button.
   */
  const handlePrint = () => {
    frameRef.current?.contentWindow?.print();
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-4xl max-h-[90vh] overflow-hidden flex flex-col">
        <DialogHeader className="flex-row items-center justify-between gap-4 space-y-0 pr-10">
          <DialogTitle>Report Preview</DialogTitle>
          {html && (
            <Button
              variant="outline"
              size="sm"
              disabled={!frameReady}
              onClick={handlePrint}
            >
              <Printer className="mr-2 h-4 w-4" />
              Print / Save as PDF
            </Button>
          )}
        </DialogHeader>
        <div className="flex-1 overflow-auto">
          {isLoading ? (
            <div className="py-8 text-center text-muted-foreground">
              Loading report...
            </div>
          ) : html ? (
            <iframe
              ref={frameRef}
              srcDoc={html}
              title="Report Preview"
              className="w-full h-[70vh] border-0 rounded"
              // allow-scripts is deliberately absent: the report is static
              // markup, so nothing inside it needs to run, and allow-modals
              // grants the parent's print() call without granting the document
              // any ability to open dialogs of its own.
              sandbox="allow-same-origin allow-modals"
              onLoad={() => {
                setFrameReady(true);
              }}
            />
          ) : (
            <div className="py-8 text-center text-muted-foreground">
              No report data available.
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
