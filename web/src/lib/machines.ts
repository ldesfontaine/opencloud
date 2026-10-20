import type { Machine } from "../api/machines";
import type { Translate } from "../i18n/context";
import { seenAgo } from "./time";

// Le sous-titre des pages qui comptent les machines : rien, une, plusieurs.
export function machinesSubtitle(t: Translate, total: number, online: number): string {
  if (total === 0) {
    return t("overview.subtitle");
  }
  if (total === 1) {
    return t("machines.subtitle_one", online);
  }
  return t("machines.subtitle", total, online);
}

// Le formulaire d'une machine neuve : l'opérateur la nomme, le serveur
// répond par un jeton. Le corps de la requête est ce nom, tel quel.
export interface MachineFormValues {
  name: string;
}

export const emptyMachineForm: MachineFormValues = { name: "" };

// « vu » d'une machine. En ligne, elle l'est à l'instant : son flux est
// ouvert. Le direct ne republie pas la liste à chaque signal, le dernier
// signal lu vieillirait à l'écran pendant que la machine répond.
export function machineSeen(t: Translate, machine: Pick<Machine, "online" | "last_seen_at">, now: number): string {
  if (machine.online) {
    return t("machine.seen_now");
  }
  return seenAgo(t, machine.last_seen_at, now);
}
