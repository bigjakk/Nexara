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

export function ReportPreview({ runId, open, onOpenChange }: ReportPreviewProps) {
  const { data: html, isLoading } = useReportRunHTML(open ? runId : "");

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-4xl max-h-[90vh] overflow-hidden flex flex-col">
        <DialogHeader>
          <DialogTitle>Report Preview</DialogTitle>
        </DialogHeader>
        <div className="flex-1 overflow-auto">
          {isLoading ? (
            <div className="py-8 text-center text-muted-foreground">Loading report...</div>
          ) : html ? (
            <iframe
              srcDoc={html}
              title="Report Preview"
              className="w-full h-[70vh] border-0 rounded"
              sandbox="allow-same-origin"
            />
          ) : (
            <div className="py-8 text-center text-muted-foreground">No report data available.</div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
