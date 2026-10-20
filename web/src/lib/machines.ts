import type { Translate } from "../i18n/context";

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
