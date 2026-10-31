import { knownMachineTabs } from "./modules";

// Les onglets promis mais pas encore livrés : masqués, leur adresse mène
// au résumé de la machine pour qu'un lien gardé ne tombe pas sur une page
// vide.
export const unreleasedMachineTabs = ["sauvegardes", "reglages"];

// Les onglets qui ont changé d'adresse : l'ancien segment mène au nouveau, pour
// qu'un lien gardé suive (« Domaines et certificats » est devenu « Disponibilité »).
export const renamedMachineTabs: Record<string, string> = { domaines: "disponibilite" };

// Ce que devient un segment d'URL : un onglet connu (porté ou non par la
// machine : son adresse reste servie), un renvoi au résumé, un renvoi vers son
// nouveau nom, ou une page introuvable.
export function resolveMachineTab(slug: string): "shown" | "summary" | "renamed" | "unknown" {
  if (knownMachineTabs.some((tab) => tab.slug === slug)) {
    return "shown";
  }
  if (slug in renamedMachineTabs) {
    return "renamed";
  }
  if (unreleasedMachineTabs.includes(slug)) {
    return "summary";
  }
  return "unknown";
}
