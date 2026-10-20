import type { Alert } from "../api/alerts";
import type { Translate } from "../i18n/context";

// Un groupe de la liste des alertes : une machine, ou rien pour ce qui
// n'en a pas (tâche, sonde sans machine). L'ordre des alertes reçu du
// serveur, les plus graves en tête, est gardé dans chaque groupe.
export interface AlertGroup {
  machineID: string;
  machineName: string;
  alerts: Alert[];
}

// Les groupes viennent dans l'ordre de leur première alerte : la machine
// qui porte l'alerte la plus grave reste en tête.
export function groupByMachine(alerts: Alert[]): AlertGroup[] {
  const groups: AlertGroup[] = [];
  const byMachine = new Map<string, AlertGroup>();
  for (const alert of alerts) {
    let group = byMachine.get(alert.machine_id);
    if (group === undefined) {
      group = { machineID: alert.machine_id, machineName: alert.machine_name, alerts: [] };
      byMachine.set(alert.machine_id, group);
      groups.push(group);
    }
    group.alerts.push(alert);
  }
  return groups;
}

// Ce qu'on peut cocher : les ouvertes que personne n'a acquittées.
export function selectableIDs(alerts: Alert[]): number[] {
  return alerts.filter((alert) => alert.status === "open" && alert.acknowledged_at === null).map((alert) => alert.id);
}

// La sélection ne garde que ce qui est encore cochable : une alerte
// acquittée ailleurs ou résolue en direct sort d'elle-même.
// Rend la même sélection quand rien ne sort : React ne relance alors pas
// le rendu.
export function keepSelectable(selected: Set<number>, alerts: Alert[]): Set<number> {
  const selectable = new Set(selectableIDs(alerts));
  const kept = [...selected].filter((id) => selectable.has(id));
  return kept.length === selected.size ? selected : new Set(kept);
}

// Coche ou décoche tout un ensemble : s'il est déjà entièrement coché, on
// le décoche ; sinon on le complète.
export function toggleAll(selected: Set<number>, ids: number[]): Set<number> {
  const next = new Set(selected);
  const allSelected = ids.length > 0 && ids.every((id) => next.has(id));
  for (const id of ids) {
    if (allSelected) {
      next.delete(id);
    } else {
      next.add(id);
    }
  }
  return next;
}

// L'état d'une case qui couvre un ensemble : cochée, vide, ou entre les deux.
export function coverage(selected: Set<number>, ids: number[]): "all" | "some" | "none" {
  const count = ids.filter((id) => selected.has(id)).length;
  if (count === 0) {
    return "none";
  }
  return count === ids.length ? "all" : "some";
}

// Le retour après un acquittement groupé, au nombre près.
export function acknowledgedMessage(t: Translate, acknowledged: number): string {
  if (acknowledged === 0) {
    return t("alerts.acknowledged_none");
  }
  if (acknowledged === 1) {
    return t("alerts.acknowledged_one");
  }
  return t("alerts.acknowledged_many", acknowledged);
}

export function selectedLabel(t: Translate, count: number): string {
  return count === 1 ? t("alerts.selected_one") : t("alerts.selected", count);
}

export function groupCountLabel(t: Translate, count: number): string {
  return count === 1 ? t("alerts.group_count_one") : t("alerts.group_count", count);
}
