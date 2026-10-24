export interface Tab {
  slug: string;
  key: string;
}

// Les onglets d'une machine : segment d'URL → clé de libellé. Seul ce qui
// est livré s'affiche.
export const machineTabs: Tab[] = [
  { slug: "", key: "tab.summary" },
  { slug: "services", key: "tab.services" },
  { slug: "disponibilite", key: "tab.availability" },
  { slug: "reseau", key: "tab.network" },
  { slug: "ssh", key: "tab.ssh" },
  { slug: "journaux", key: "tab.logs" },
];

// L'onglet de l'hyperviseur : il ne s'affiche que sur une machine qui porte un
// hôte Proxmox déclaré. Son adresse reste servie sans hôte (une carte y mène à
// la déclaration), pour qu'un lien gardé ne tombe pas sur une page introuvable.
export const hypervisorTab: Tab = { slug: "hyperviseur", key: "tab.hypervisor" };

// Les onglets affichés pour une machine : les onglets fixes, puis
// l'hyperviseur quand un hôte est déclaré, avant les journaux.
export function visibleMachineTabs(hostsProxmox: boolean): Tab[] {
  if (!hostsProxmox) {
    return machineTabs;
  }
  const logs = machineTabs.findIndex((tab) => tab.slug === "journaux");
  return [...machineTabs.slice(0, logs), hypervisorTab, ...machineTabs.slice(logs)];
}

// Les onglets promis mais pas encore livrés : masqués, leur adresse mène
// au résumé de la machine pour qu'un lien gardé ne tombe pas sur une page
// vide.
export const unreleasedMachineTabs = ["sauvegardes", "reglages"];

// Les onglets qui ont changé d'adresse : l'ancien segment mène au nouveau, pour
// qu'un lien gardé suive (« Domaines et certificats » est devenu « Disponibilité »).
export const renamedMachineTabs: Record<string, string> = { domaines: "disponibilite" };

// Ce que devient un segment d'URL : un onglet affiché, un renvoi au résumé,
// un renvoi vers son nouveau nom, ou une page introuvable.
export function resolveMachineTab(slug: string): "shown" | "summary" | "renamed" | "unknown" {
  if (slug === hypervisorTab.slug || machineTabs.some((tab) => tab.slug === slug)) {
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
