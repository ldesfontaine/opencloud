import type { Counts } from "../api/counts";
import type { Machine } from "../api/machines";
import type { IconName } from "../components/Icon";

// Le registre des modules : seul lieu qui sait qu'un module a une entrée de
// menu, un onglet de fiche ou un premier geste de configuration. Le serveur
// dit quels modules existent et lesquels une machine ou l'installation porte
// (internal/module) ; ici, ce que l'écran en fait.

export type ModuleID = "containers" | "hypervisor" | "guest" | "ssh_keys" | "probes" | "firewall" | "backups" | "jobs";

// Un chiffre du menu, et s'il demande attention (rouge).
interface Counter {
  value: number;
  hot: boolean;
}

interface MenuDef {
  href: string;
  icon: IconName;
  rank: number;
  counter?: (counts: Counts) => Counter | null;
  // Le premier geste du module, là où Paramètres › Modules et la vue d'ensemble
  // envoient quand rien n'est encore configuré.
  setup: string;
}

interface TabDef {
  slug: string;
  rank: number;
}

// Un onglet de la page Machines, pour les modules dont la vue d'ensemble porte
// sur toutes les machines qui les portent.
interface ListTabDef {
  href: string;
  label: string;
  rank: number;
}

interface ModuleDef {
  id: ModuleID;
  // Un module de l'installation a une entrée de menu dès le premier lancement :
  // on cherche « Disponibilité » là où elle sera, pas dans une carte qui
  // disparaît une fois le module configuré (audit G3).
  menu?: MenuDef;
  // Un module de machine a un onglet sur la fiche des machines qui le portent.
  tab?: TabDef;
  // Un module de machine peut aussi avoir une vue globale, en onglet de Machines.
  listTab?: ListTabDef;
}

// Le chiffre de Disponibilité : les sondes, en rouge quand l'une est hors
// ligne ou qu'un incident est ouvert. Sans sonde, les incidents ouverts
// seuls parlent.
function availabilityCounter(counts: Counts): Counter | null {
  const hot = counts.probes.attention > 0 || counts.status.open_incidents > 0;
  if (counts.probes.total > 0) {
    return { value: counts.probes.total, hot };
  }
  return counts.status.open_incidents > 0 ? { value: counts.status.open_incidents, hot: true } : null;
}

// Les rangs rangent le menu et les onglets ; ils sont espacés pour qu'une
// entrée fixe s'intercale (menuFixedEntries, fixedTabs).
const modules: ModuleDef[] = [
  { id: "containers", tab: { slug: "services", rank: 10 } },
  { id: "hypervisor", tab: { slug: "hyperviseur", rank: 50 } },
  { id: "guest" },
  { id: "ssh_keys", tab: { slug: "ssh", rank: 40 }, listTab: { href: "/machines/acces-ssh", label: "machines.tab_ssh", rank: 10 } },
  {
    id: "probes",
    tab: { slug: "disponibilite", rank: 20 },
    menu: {
      href: "/disponibilite",
      icon: "globe",
      rank: 60,
      counter: availabilityCounter,
      setup: "/disponibilite/nouvelle",
    },
  },
  { id: "firewall", menu: { href: "/pare-feu", icon: "firewall", rank: 30, setup: "/pare-feu/nouveau" } },
  { id: "backups", menu: { href: "/sauvegardes", icon: "archive", rank: 40, setup: "/sauvegardes" } },
  {
    id: "jobs",
    menu: {
      href: "/taches",
      icon: "clock",
      rank: 70,
      counter: (counts) => (counts.jobs.attention > 0 ? { value: counts.jobs.attention, hot: true } : null),
      setup: "/taches/nouvelle",
    },
  },
];

// Une entrée du menu : celle d'un module configuré, ou une entrée fixe.
export interface MenuEntry {
  key: string;
  label: string;
  href: string;
  icon: IconName;
  counter: Counter | null;
  rank: number;
  // Un module sans aucun objet : un point le signale (« à configurer »). Faux
  // tant que les compteurs ne sont pas lus : on n'invente pas un état.
  toConfigure: boolean;
}

// Les entrées qui ne sont pas des modules.
const fixedEntries: { key: string; label: string; href: string; icon: IconName; rank: number; counter?: (counts: Counts) => Counter | null }[] = [
  { key: "overview", label: "nav.overview", href: "/", icon: "grid", rank: 10 },
  {
    key: "machines",
    label: "nav.machines",
    href: "/machines",
    icon: "server",
    rank: 20,
    counter: (counts) => (counts.machines.total > 0 ? { value: counts.machines.total, hot: counts.machines.online < counts.machines.total } : null),
  },
  {
    key: "services",
    label: "nav.services",
    href: "/services",
    icon: "box",
    rank: 50,
    counter: (counts) => (counts.containers.total > 0 ? { value: counts.containers.total, hot: counts.containers.attention > 0 } : null),
  },
  {
    key: "alerts",
    label: "nav.alerts",
    href: "/alertes",
    icon: "bell",
    rank: 80,
    counter: (counts) => (counts.alerts.open > 0 ? { value: counts.alerts.open, hot: counts.alerts.unacknowledged > 0 } : null),
  },
  { key: "audit", label: "nav.audit", href: "/journal", icon: "file", rank: 90 },
];

// Paramètres n'est pas un module : il est en bas du menu, au-dessus de la langue
// et du thème.
export const settingsEntry: MenuEntry = { key: "settings", label: "nav.settings", href: "/parametres", icon: "settings", rank: 0, counter: null, toConfigure: false };

// Le menu : les entrées fixes et celles de tous les modules, configurés ou non,
// dans l'ordre des rangs. Sans compteurs lus, on ne sait pas encore lesquels
// sont configurés : aucun point ni chiffre n'est inventé.
export function menuEntries(counts: Counts | null): MenuEntry[] {
  const configured = new Set((counts?.modules ?? []).filter((state) => state.configured).map((state) => state.id));
  const entries: MenuEntry[] = fixedEntries.map((entry) => ({
    key: entry.key,
    label: entry.label,
    href: entry.href,
    icon: entry.icon,
    rank: entry.rank,
    counter: counts !== null && entry.counter !== undefined ? entry.counter(counts) : null,
    toConfigure: false,
  }));
  for (const module of modules) {
    if (module.menu === undefined) {
      continue;
    }
    entries.push({
      key: module.id,
      label: `module.${module.id}`,
      href: module.menu.href,
      icon: module.menu.icon,
      rank: module.menu.rank,
      counter: counts !== null && module.menu.counter !== undefined ? module.menu.counter(counts) : null,
      toConfigure: counts !== null && !configured.has(module.id),
    });
  }
  return entries.sort((first, second) => first.rank - second.rank);
}

// Les onglets de la page Machines : la liste, puis la vue globale de chaque
// module que l'une des machines porte.
export function machinesListTabs(machines: readonly Machine[]): { to: string; key: string }[] {
  const extra: ListTabDef[] = [];
  for (const module of modules) {
    if (module.listTab !== undefined && machines.some((machine) => machine.modules.includes(module.id))) {
      extra.push(module.listTab);
    }
  }
  extra.sort((first, second) => first.rank - second.rank);
  return [{ to: "/machines", key: "machines.tab_list" }, ...extra.map((tab) => ({ to: tab.href, key: tab.label }))];
}

// Un onglet de la fiche d'une machine : son adresse et la clé de son libellé.
interface MachineTab {
  slug: string;
  label: string;
  rank: number;
  // Le module qui l'apporte ; absent pour un onglet fixe.
  module?: ModuleID;
}

// Les onglets qui ne sont pas des modules.
const fixedTabs: MachineTab[] = [
  { slug: "", label: "tab.summary", rank: 0 },
  { slug: "reseau", label: "tab.network", rank: 30 },
    { slug: "journaux", label: "tab.logs", rank: 60 },
  { slug: "agent", label: "tab.agent", rank: 70 },
];

// Tous les onglets que la fiche sait montrer, portés ou non : l'adresse d'un
// onglet reste servie sans son module, pour qu'un lien gardé ne tombe pas sur
// une page introuvable.
export const knownMachineTabs: MachineTab[] = [
  ...fixedTabs,
  ...modules.flatMap((module) => (module.tab === undefined ? [] : [{ slug: module.tab.slug, label: `module.${module.id}`, rank: module.tab.rank, module: module.id }])),
].sort((first, second) => first.rank - second.rank);

// Les onglets d'une machine : les fixes, et ceux des modules qu'elle porte.
export function machineTabsOf(carried: readonly ModuleID[]): MachineTab[] {
  return knownMachineTabs.filter((tab) => tab.module === undefined || carried.includes(tab.module));
}

// Une chose à configurer : un module de l'installation sans objet, ou le
// module d'une machine dont le rôle l'annonce sans que rien soit déclaré.
export type Todo =
  | { kind: "installation"; module: ModuleID; href: string }
  | { kind: "hypervisor"; module: "hypervisor"; machineName: string; href: string };

// Ce que la vue d'ensemble signale. L'hôte Proxmox d'une machine au rôle
// Proxmox se déclare dans son module : tant qu'il ne l'est pas, c'est à faire.
export function modulesToConfigure(counts: Counts | null, machines: readonly Machine[]): Todo[] {
  const todos: Todo[] = [];
  for (const state of counts?.modules ?? []) {
    const menu = modules.find((module) => module.id === state.id)?.menu;
    if (!state.configured && menu !== undefined) {
      todos.push({ kind: "installation", module: state.id, href: menu.setup });
    }
  }
  for (const machine of machines) {
    if (machine.modules.includes("hypervisor") && !machine.proxmox.declared) {
      todos.push({ kind: "hypervisor", module: "hypervisor", machineName: machine.name, href: `/machines/${machine.id}/hyperviseur` });
    }
  }
  return todos;
}

// Où ouvrir un module de l'installation configuré (Paramètres › Modules).
export function moduleHref(id: ModuleID): string | null {
  return modules.find((module) => module.id === id)?.menu?.href ?? null;
}

// Où le configurer quand il ne l'est pas.
export function moduleSetupHref(id: ModuleID): string | null {
  return modules.find((module) => module.id === id)?.menu?.setup ?? null;
}
