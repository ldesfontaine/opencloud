import type { Machine } from "../api/machines";
import { agentBehindServer } from "./agentUpdate";
import { isArchived } from "./machines";
import { detectedProxmox } from "./proxmox";

// Ce qui lève un type « inconnu », dans l'ordre où l'opérateur doit s'y prendre.
type KindFixAction = "reenroll" | "update_agent" | "declare_host" | "correct";

export interface KindFix {
  action: KindFixAction;
  // Là où le geste se fait.
  to: string;
}

// Le geste qui résout un type inconnu, null quand le type est connu (ou corrigé
// à la main) ou que la machine est archivée. Le serveur ne sait pas le type
// quand l'agent ne le rapporte pas (agent trop ancien, ou sans systemd) et
// qu'aucun hôte déclaré ne rapproche la machine d'un invité.
// L'hôte à déclarer est une autre machine : la première dont l'agent a reconnu
// Proxmox sans qu'il soit déclaré.
export function kindFix(machine: Machine, all: readonly Machine[], serverVersion: string): KindFix | null {
  if (machine.kind !== "unknown" || machine.kind_override !== "" || isArchived(machine)) {
    return null;
  }
  const base = `/machines/${machine.id}`;
  if (machine.agent === null) {
    return { action: "reenroll", to: `${base}/reenroller` };
  }
  if (agentBehindServer(machine.agent.version, serverVersion)) {
    return { action: "update_agent", to: `${base}/agent` };
  }
  const undeclared = all.find((other) => other.id !== machine.id && !other.proxmox.declared && detectedProxmox(other) !== null);
  if (undeclared !== undefined) {
    return { action: "declare_host", to: `/machines/${undeclared.id}/proxmox` };
  }
  return { action: "correct", to: `${base}/emplacement` };
}
