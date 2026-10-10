export interface Tab {
  slug: string;
  key: string;
}

// Les onglets d'une machine : segment d'URL → clé de libellé. Seul ce qui
// est livré s'affiche.
export const machineTabs: Tab[] = [
  { slug: "", key: "tab.summary" },
  { slug: "services", key: "tab.services" },
  { slug: "domaines", key: "tab.domains" },
  { slug: "reseau", key: "tab.network" },
  { slug: "journaux", key: "tab.logs" },
];

// Les onglets promis mais pas encore livrés : masqués, leur adresse mène
// au résumé de la machine pour qu'un lien gardé ne tombe pas sur une page
// vide.
export const unreleasedMachineTabs = ["sauvegardes", "reglages"];

// Ce que devient un segment d'URL : un onglet affiché, un renvoi au résumé,
// ou une page introuvable.
export function resolveMachineTab(slug: string): "shown" | "summary" | "unknown" {
  if (machineTabs.some((tab) => tab.slug === slug)) {
    return "shown";
  }
  if (unreleasedMachineTabs.includes(slug)) {
    return "summary";
  }
  return "unknown";
}
