import type { ObservedUnit } from "../api/directory";
import type { SystemdActive, SystemdUnit } from "../api/systemd";
import type { Tone } from "./tone";

// Les vues du filtre de l'onglet, dans l'ordre du menu.
export const systemdViews = ["all", "running", "failed", "declared"] as const;
export type SystemdView = (typeof systemdViews)[number];

const activeTones: Record<SystemdActive, Tone> = {
  active: "ok",
  inactive: "neutral",
  failed: "danger",
  activating: "warn",
  deactivating: "warn",
  reloading: "warn",
  other: "neutral",
};

// La couleur d'un état d'unité : en marche vert, en échec rouge, le reste
// neutre ou orange. Le mot reste dans la pastille.
export function activeTone(active: string): Tone {
  return activeTones[active as SystemdActive] ?? "neutral";
}

// Une unité sort-elle dans la vue choisie, pour le texte tapé ? Le texte
// cherche dans le nom, sans casse.
export function isShown(unit: SystemdUnit, view: SystemdView, query: string): boolean {
  if (query.trim() !== "" && !unit.name.toLowerCase().includes(query.trim().toLowerCase())) {
    return false;
  }
  switch (view) {
    case "running":
      return unit.active === "active";
    case "failed":
      return unit.active === "failed";
    case "declared":
      return unit.declared !== null;
    default:
      return true;
  }
}

// Un binaire déclaré sans port : l'invitation « Déclarer le port ». Le relevé
// systemd ne dit pas sur quel port il écoute, l'opérateur le dit.
export function needsPort(kind: "container" | "binary", ports: readonly string[]): boolean {
  return kind === "binary" && ports.length === 0;
}

// L'état d'une unité observée à écrire dans une pastille : la clé du libellé
// et la couleur. Une unité absente ou non relevée se dit par sa présence.
export function observedPill(observed: ObservedUnit): { key: string; tone: Tone } {
  switch (observed.presence) {
    case "found":
      return { key: `systemd.active_${observed.active}`, tone: activeTone(observed.active) };
    case "missing":
      return { key: "systemd.presence_missing", tone: "danger" };
    default:
      return { key: "systemd.presence_unknown", tone: "neutral" };
  }
}

// Où ouvrir la déclaration d'une unité relevée : l'assistant, rempli par le
// serveur d'après le relevé de la machine.
export function declareUnitPath(machineID: string, unitName: string): string {
  return `/services/annuaire/nouveau?machine=${encodeURIComponent(machineID)}&unit=${encodeURIComponent(unitName)}`;
}

// Les machines dont l'agent a relevé les unités systemd : celles qui ont
// l'onglet des unités, où un binaire se choisit pour être déclaré.
export function machinesWithUnits<Item extends { modules: readonly string[] }>(machines: readonly Item[]): Item[] {
  return machines.filter((machine) => machine.modules.includes("systemd_units"));
}
