import { useEffect } from "react";
import { TerminalSquare } from "lucide-react";
import { useConsoleStore } from "@/stores/console-store";
import { QuickConnect } from "../components/QuickConnect";

export function ConsolePage() {
  const setWindowMode = useConsoleStore((s) => s.setWindowMode);
  const tabs = useConsoleStore((s) => s.tabs);

  useEffect(() => {
    if (tabs.length > 0) {
      setWindowMode("maximized");
    }
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="flex h-full items-center justify-center text-muted-foreground">
      <div className="space-y-2 text-center">
        <TerminalSquare className="mx-auto h-12 w-12 opacity-30" />
        <p>Console sessions run in the floating window.</p>
        {tabs.length === 0 && (
          <div className="flex justify-center pt-2">
            <QuickConnect />
          </div>
        )}
      </div>
    </div>
  );
}
