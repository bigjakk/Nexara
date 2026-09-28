import { useState } from "react";

/**
 * Whether the operator has confirmed passing a device through despite
 * `risks` (pciPassRisks), for the pick `pickKey` names. The tick holds only
 * for the pick it was given for, and for what was said then: it is keyed by
 * both, so a warning that arrives after the tick — the node's device list
 * loading late — is asked about afresh. With no risks there is nothing to
 * confirm.
 */
export function usePCIRiskAck(pickKey: string, risks: readonly string[]) {
  const riskKey = risks.length > 0 ? `${pickKey}|${risks.join("\n")}` : "";
  const [acknowledged, setAcknowledged] = useState("");
  return {
    accepted: riskKey === "" || acknowledged === riskKey,
    setAccepted: (accepted: boolean) => {
      setAcknowledged(accepted ? riskKey : "");
    },
  };
}
