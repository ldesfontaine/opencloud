import { expect, test } from "vitest";

import type { Counts } from "../api/counts";
import type { Machine } from "../api/machines";
import { knownMachineTabs, machineTabsOf, machinesListTabs, menuEntries, moduleHref, modulesToConfigure, moduleSetupHref, type ModuleID } from "./modules";

function countsWith(configured: ModuleID[]): Counts {
  const all: ModuleID[] = ["probes", "firewall", "backups", "jobs"];
  return {
    machines: { total: 3, online: 2 },
    jobs: { total: 2, attention: 1 },
    containers: { total: 4, attention: 0, unknown: 0, updates: 0 },
    probes: { total: 5, attention: 2 },
    certificates: { total: 0, expiring: 0, expired: 0, soonest_expires_at: null },
    status: { open_incidents: 1 },
    alerts: { open: 3, unacknowledged: 1 },
    modules: all.map((id) => ({ id, configured: configured.includes(id) })),
  };
}

function machineWith(name: string, modules: ModuleID[], declared = false): Machine {
  return { id: `id-${name}`, name, modules, proxmox: { declared, guests: 0, last_error: "", hosted: null } } as Machine;
}

const labels = (entries: ReturnType<typeof menuEntries>) => entries.map((entry) => entry.key);

test("sans compteurs lus, le menu ne montre que les entrées fixes, sans chiffre inventé", () => {
  const entries = menuEntries(null);
  expect(labels(entries)).toEqual(["overview", "machines", "services", "alerts", "audit"]);
  expect(entries.every((entry) => entry.counter === null)).toBe(true);
});

test("un module n'entre au menu que configuré, à son rang", () => {
  expect(labels(menuEntries(countsWith([])))).toEqual(["overview", "machines", "services", "alerts", "audit"]);
  expect(labels(menuEntries(countsWith(["jobs", "firewall", "probes"])))).toEqual([
    "overview",
    "machines",
    "firewall",
    "services",
    "probes",
    "jobs",
    "alerts",
    "audit",
  ]);
});

test("le pare-feu et les sauvegardes sont au niveau de Machines, avant Services", () => {
  const keys = labels(menuEntries(countsWith(["backups", "firewall", "probes", "jobs"])));
  expect(keys.indexOf("machines")).toBeLessThan(keys.indexOf("firewall"));
  expect(keys.indexOf("firewall")).toBeLessThan(keys.indexOf("backups"));
  expect(keys.indexOf("backups")).toBeLessThan(keys.indexOf("services"));
  expect(keys.indexOf("services")).toBeLessThan(keys.indexOf("probes"));
  expect(keys.indexOf("probes")).toBeLessThan(keys.indexOf("jobs"));
  expect(keys.indexOf("jobs")).toBeLessThan(keys.indexOf("alerts"));
});

test("les compteurs : machines en rouge dès qu'une est hors ligne, tâches à traiter, alertes non acquittées", () => {
  const byKey = new Map(menuEntries(countsWith(["jobs", "probes"])).map((entry) => [entry.key, entry.counter]));
  expect(byKey.get("machines")).toEqual({ value: 3, hot: true });
  expect(byKey.get("jobs")).toEqual({ value: 1, hot: true });
  expect(byKey.get("probes")).toEqual({ value: 5, hot: true });
  expect(byKey.get("services")).toEqual({ value: 4, hot: false });
  expect(byKey.get("alerts")).toEqual({ value: 3, hot: true });
  expect(byKey.get("overview")).toBeNull();
});

// Disponibilité réunit les sondes et les pages publiques sous une seule entrée :
// le chiffre est celui des sondes, rouge pour une sonde hors ligne ou un
// incident ouvert ; sans sonde, les incidents ouverts parlent seuls.
test("Disponibilité : un seul chiffre pour les sondes et les incidents", () => {
  const hot = (counts: Counts) => new Map(menuEntries(counts).map((entry) => [entry.key, entry.counter])).get("probes");
  const base = countsWith(["probes"]);
  expect(hot({ ...base, probes: { total: 5, attention: 0 }, status: { open_incidents: 0 } })).toEqual({ value: 5, hot: false });
  expect(hot({ ...base, probes: { total: 5, attention: 0 }, status: { open_incidents: 2 } })).toEqual({ value: 5, hot: true });
  expect(hot({ ...base, probes: { total: 0, attention: 0 }, status: { open_incidents: 2 } })).toEqual({ value: 2, hot: true });
  expect(hot({ ...base, probes: { total: 0, attention: 0 }, status: { open_incidents: 0 } })).toBeNull();
});

test("les onglets d'une machine : les fixes, puis ceux des modules qu'elle porte, dans l'ordre", () => {
  const slugs = (carried: ModuleID[]) => machineTabsOf(carried).map((tab) => tab.slug);
  expect(slugs([])).toEqual(["", "reseau", "journaux", "agent"]);
  expect(slugs(["containers", "probes", "ssh_keys"])).toEqual(["", "services", "disponibilite", "reseau", "ssh", "journaux", "agent"]);
  expect(slugs(["hypervisor"])).toEqual(["", "reseau", "hyperviseur", "journaux", "agent"]);
  expect(slugs(["guest"])).toEqual(["", "reseau", "journaux", "agent"]);
});

test("chaque onglet connu a son libellé : fixe, ou celui de son module", () => {
  for (const tab of knownMachineTabs) {
    expect(tab.label.startsWith(tab.module === undefined ? "tab." : "module.")).toBe(true);
  }
});

test("la vue d'ensemble signale les modules sans objet, et l'hôte Proxmox d'une machine au rôle Proxmox", () => {
  const todos = modulesToConfigure(countsWith(["jobs"]), [machineWith("pve", ["hypervisor"]), machineWith("declared", ["hypervisor"], true), machineWith("dock", ["containers"])]);
  expect(todos.map((todo) => todo.module)).toEqual(["probes", "firewall", "backups", "hypervisor"]);
  const hypervisor = todos.at(-1);
  expect(hypervisor).toMatchObject({ kind: "hypervisor", machineName: "pve", href: "/machines/id-pve/hyperviseur" });
  expect(modulesToConfigure(countsWith(["probes", "firewall", "backups", "jobs"]), [])).toEqual([]);
  expect(modulesToConfigure(null, [])).toEqual([]);
});

test("Paramètres › Modules sait où ouvrir et où configurer un module de l'installation, et rien pour un module de machine", () => {
  expect(moduleHref("firewall")).toBe("/pare-feu");
  expect(moduleSetupHref("jobs")).toBe("/taches/nouvelle");
  expect(moduleHref("containers")).toBeNull();
  expect(moduleSetupHref("hypervisor")).toBeNull();
});

test("Machines n'a d'onglet que pour les modules que l'une des machines porte", () => {
  expect(machinesListTabs([machineWith("a", ["containers"])])).toEqual([{ to: "/machines", key: "machines.tab_list" }]);
  expect(machinesListTabs([machineWith("a", ["containers"]), machineWith("b", ["ssh_keys"])])).toEqual([
    { to: "/machines", key: "machines.tab_list" },
    { to: "/machines/acces-ssh", key: "machines.tab_ssh" },
  ]);
});
