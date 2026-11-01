import type { ReactNode } from "react";

import { useMachines } from "../api/machines";
import { useT } from "../i18n/context";
import { offlineAmong } from "../lib/agentReach";
import "./AgentGate.scss";

// Les machines hors ligne parmi celles demandées ; null tant que la liste
// n'est pas lue (ce qui part à l'ouverture d'un écran, comme les journaux,
// attend de savoir).
export function useOfflineMachines(wanted: readonly string[]): string[] | null {
  const machines = useMachines();
  return machines.data === null ? null : offlineAmong(machines.data.machines, wanted);
}

// Le point commun des gestes qui exigent l'agent connecté : si l'une des
// machines est hors ligne, le geste se désactive (blocked) et la raison se
// dit à côté, pas seulement au survol. Chaque écran passe son bouton en
// enfant ; aucun ne refait le test ni le message.
export function AgentGate({ machineIDs, children }: { machineIDs: readonly string[]; children: (blocked: boolean) => ReactNode }) {
  const t = useT();
  const blocked = (useOfflineMachines(machineIDs) ?? []).length > 0;
  return (
    <span className="agent-gate">
      {children(blocked)}
      {blocked && (
        <span className="agent-gate-reason secondary" role="note">
          {t("agent.offline_reason")}
        </span>
      )}
    </span>
  );
}
