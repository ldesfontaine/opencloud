import { expect, test } from "vitest";

import type { Machine } from "../api/machines";
import type { Translate } from "../i18n/context";
import { backupProducer, canConfirmDeletion, capabilitiesOf, isArchived, liveMachines, offeredCapabilities, capabilityLabelKey, capabilityNames, describeAccount, describeCapability, describeUnavailable, emptyMachineForm, isMachineFormModified, isRightsModified, kindsByID, machineOption, machineSeen, machinesSubtitle, rightsFormOf, rolePatch, toggleRight, unavailableRights } from "./machines";

const t: Translate = (key, ...args) => [key, ...args].join(":");

test("machinesSubtitle dit l'accueil sans machine, puis le compte et celles en ligne", () => {
  expect(machinesSubtitle(t, 0, 0)).toBe("overview.subtitle");
  expect(machinesSubtitle(t, 1, 1)).toBe("machines.subtitle_one:1");
  expect(machinesSubtitle(t, 4, 3)).toBe("machines.subtitle:4:3");
});

test("machineSeen : en ligne, vue à l'instant ; hors ligne, depuis son dernier signal", () => {
  const now = Date.parse("2026-09-12T12:00:00Z");
  expect(machineSeen(t, { online: true, last_seen_at: "2026-09-12T11:50:00Z" }, now)).toBe("machine.seen_now");
  expect(machineSeen(t, { online: false, last_seen_at: "2026-09-12T11:59:48Z" }, now)).toBe("machine.seen_ago:time.seconds:12");
  expect(machineSeen(t, { online: false, last_seen_at: null }, now)).toBe("machine.never_seen");
});

test("capabilitiesOf : les droits cochés dans l'ordre de la liste, avec leurs chemins, sans observe", () => {
  const values = { name: "vps", rights: ["files.write", "observe", "agent.update", "backup.receive"], backupPath: " /srv/backups ", writePaths: "/srv/a, ,/srv/b" };
  expect(capabilitiesOf(values)).toEqual([
    { name: "agent.update", paths: [] },
    { name: "backup.receive", paths: ["/srv/backups"] },
    { name: "files.write", paths: ["/srv/a", "/srv/b"] },
  ]);
  expect(capabilitiesOf({ ...values, rights: [] })).toEqual([]);
});

test("backup.produce : une case, sa portée est le producteur unique, sans champ à remplir", () => {
  const values = { name: "pve", rights: ["backup.produce"], backupPath: "", writePaths: "" };
  expect(capabilitiesOf(values)).toEqual([{ name: "backup.produce", paths: [backupProducer] }]);
  expect(backupProducer).toBe("proxmox-vzdump");
  expect(capabilityLabelKey("backup.produce")).toBe("machine.capability_backup_produce");
  // Il se range après backup.receive, comme le serveur.
  expect(capabilityNames.indexOf("backup.produce")).toBe(capabilityNames.indexOf("backup.receive") + 1);
  // Un agent qui l'a repart avec la case cochée, et la portée refaite.
  const form = rightsFormOf([{ name: "observe", paths: [] }, { name: "backup.produce", paths: [backupProducer] }]);
  expect(form.rights).toEqual(["backup.produce"]);
  expect(capabilitiesOf(form)).toEqual([{ name: "backup.produce", paths: [backupProducer] }]);
  expect(describeUnavailable(t, "sudo_denied", null)).toBe("machine.capability_unavailable_sudo_denied");
});

test("toggleRight coche puis décoche ; la clé d'un libellé remplace le point", () => {
  expect(toggleRight(toggleRight([], "docker.deploy"), "docker.deploy")).toEqual([]);
  expect(capabilityLabelKey("files.write")).toBe("machine.capability_files_write");
  expect(describeCapability(t, { name: "files.write", paths: ["/a", "/b"] })).toBe("machine.right_with_paths:machine.capability_files_write:/a, /b");
  expect(describeCapability(t, { name: "observe", paths: [] })).toBe("machine.capability_observe");
});

test("rightsFormOf : les droits d'un agent reviennent cochés, chemins compris, et repartent tels quels", () => {
  const requested = [
    { name: "observe", paths: [] },
    { name: "agent.update", paths: [] },
    { name: "files.write", paths: ["/opt/site", "/srv/workspace"] },
  ];
  const values = rightsFormOf(requested);
  expect(values).toEqual({ rights: ["agent.update", "files.write"], backupPath: "", writePaths: "/opt/site, /srv/workspace" });
  expect(capabilitiesOf(values)).toEqual(requested.slice(1));
});

test("probe.lan : coché dans le formulaire neuf, jamais coché pour un agent qui ne l'a pas demandé", () => {
  expect(emptyMachineForm.rights).toEqual(["probe.lan"]);
  expect(capabilitiesOf(emptyMachineForm)).toEqual([{ name: "probe.lan", paths: [] }]);
  expect(capabilityLabelKey("probe.lan")).toBe("machine.capability_probe_lan");
  expect(rightsFormOf([{ name: "observe", paths: [] }]).rights).toEqual([]);
  const both = rightsFormOf([{ name: "observe", paths: [] }, { name: "probe.lan", paths: [] }, { name: "agent.update", paths: [] }]);
  expect(both.rights).toEqual(["probe.lan", "agent.update"]);
  // Décoché, il part de la requête : le serveur ne l'ajoute pas de lui-même.
  expect(capabilitiesOf({ ...emptyMachineForm, rights: [] })).toEqual([]);
});

test("describeAccount : le nom et l'uid, l'uid seul sans nom, rien tant que l'agent n'a rien dit", () => {
  expect(describeAccount({ uid: 998, name: "opencloud-agent" })).toBe("opencloud-agent (998)");
  expect(describeAccount({ uid: 0, name: "root" })).toBe("root (0)");
  expect(describeAccount({ uid: 4000, name: "" })).toBe("4000");
  expect(describeAccount(null)).toBeNull();
});

test("describeUnavailable : une phrase par raison, celle d'un dossier refusé nomme le compte, ou change sous root", () => {
  expect(describeUnavailable(t, "docker_denied", null)).toBe("machine.capability_unavailable_docker_denied");
  expect(describeUnavailable(t, "path_unwritable", { uid: 998, name: "svc" })).toBe("machine.capability_unavailable_path_unwritable");
  expect(describeUnavailable(t, "path_denied", { uid: 998, name: "svc" })).toBe("machine.capability_unavailable_path_denied:svc");
  expect(describeUnavailable(t, "path_denied", { uid: 4000, name: "" })).toBe("machine.capability_unavailable_path_denied:4000");
  expect(describeUnavailable(t, "path_denied", { uid: 0, name: "root" })).toBe("machine.capability_unavailable_path_denied_root");
});

test("unavailableRights : seuls les droits que le compte ne peut pas exercer, avec leur raison", () => {
  const agent = {
    run_as: { uid: 998, name: "svc" },
    capabilities: [
      { name: "observe", paths: [], unavailable: "" },
      { name: "docker.deploy", paths: [], unavailable: "docker_denied" },
      { name: "backup.receive", paths: ["/srv/backups"], unavailable: "path_denied" },
      { name: "agent.update", paths: [], unavailable: "" },
    ],
  };
  expect(unavailableRights(t, agent)).toEqual([
    "machine.capability_unavailable_item:machine.capability_docker_deploy:machine.capability_unavailable_docker_denied",
    "machine.capability_unavailable_item:machine.right_with_paths:machine.capability_backup_receive:/srv/backups:machine.capability_unavailable_path_denied:svc",
  ]);
  expect(unavailableRights(t, { run_as: null, capabilities: [{ name: "observe", paths: [], unavailable: "" }] })).toEqual([]);
});

test("les droits Proxmox se rangent en fin de liste, sans chemin, et ne se proposent qu'à un hôte Proxmox reconnu", () => {
  expect(capabilityNames.slice(-3)).toEqual(["files.write", "proxmox.power", "proxmox.snapshot"]);
  expect(capabilitiesOf({ rights: ["proxmox.snapshot", "proxmox.power"], backupPath: "", writePaths: "" })).toEqual([
    { name: "proxmox.power", paths: [] },
    { name: "proxmox.snapshot", paths: [] },
  ]);
  expect(offeredCapabilities(false, [])).not.toContain("proxmox.power");
  expect(offeredCapabilities(true, [])).toEqual([...capabilityNames]);
  // Déjà coché, il reste visible pour être décoché, même si le rôle a disparu.
  expect(offeredCapabilities(false, ["proxmox.snapshot"])).toEqual([...capabilityNames.filter((name) => name !== "proxmox.power")]);
  expect(capabilityLabelKey("proxmox.power")).toBe("machine.capability_proxmox_power");
});

test("isMachineFormModified : le formulaire neuf est propre, un nom ou un droit changé ne l'est plus", () => {
  expect(isMachineFormModified(emptyMachineForm)).toBe(false);
  expect(isMachineFormModified({ ...emptyMachineForm, name: "  " })).toBe(false);
  expect(isMachineFormModified({ ...emptyMachineForm, name: "vps-1" })).toBe(true);
  expect(isMachineFormModified({ ...emptyMachineForm, rights: ["probe.lan", "docker.deploy"] })).toBe(true);
  expect(isMachineFormModified({ ...emptyMachineForm, rights: [] })).toBe(true);
});

const sessionRoles = [
  { id: "generic" as const, capabilities: ["probe.lan"] },
  { id: "docker" as const, capabilities: ["probe.lan", "docker.deploy", "docker.update"] },
  { id: "proxmox" as const, capabilities: ["probe.lan", "proxmox.power", "proxmox.snapshot"] },
];

test("rolePatch : choisir un rôle coche ses droits d'avance, tels que le serveur les dit", () => {
  expect(rolePatch("docker", sessionRoles)).toEqual({ role: "docker", rights: ["probe.lan", "docker.deploy", "docker.update"] });
  expect(rolePatch("generic", sessionRoles)).toEqual({ role: "generic", rights: ["probe.lan"] });
  expect(capabilitiesOf({ ...emptyMachineForm, ...rolePatch("proxmox", sessionRoles) }).map((right) => right.name)).toEqual(["probe.lan", "proxmox.power", "proxmox.snapshot"]);
});

test("rolePatch : un rôle que la session ne connaît pas ne coche rien", () => {
  expect(rolePatch("docker", [])).toEqual({ role: "docker", rights: [] });
});

test("isMachineFormModified : changer de rôle est une saisie", () => {
  expect(isMachineFormModified({ ...emptyMachineForm, role: "docker" })).toBe(true);
});

test("isRightsModified : l'ordre des cases ne compte pas, un droit ou un chemin changé si", () => {
  const initial = rightsFormOf([{ name: "agent.update", paths: [] }, { name: "files.write", paths: ["/srv/a"] }]);
  expect(isRightsModified({ ...initial, rights: [...initial.rights].reverse() }, initial)).toBe(false);
  expect(isRightsModified({ ...initial, writePaths: " /srv/a " }, initial)).toBe(false);
  expect(isRightsModified({ ...initial, rights: ["agent.update"] }, initial)).toBe(true);
  expect(isRightsModified({ ...initial, writePaths: "/srv/b" }, initial)).toBe(true);
  expect(isRightsModified({ ...initial, rights: [...initial.rights, "docker.update"] }, initial)).toBe(true);
});

const archivedAgent = { agent: { revoked_at: "2026-10-31T10:00:00Z" } } as Pick<Machine, "agent">;
const activeAgent = { agent: { revoked_at: null } } as Pick<Machine, "agent">;

test("une machine est archivée quand son agent est révoqué, pas avant", () => {
  expect(isArchived(archivedAgent)).toBe(true);
  expect(isArchived(activeAgent)).toBe(false);
  expect(isArchived({ agent: null })).toBe(false);
});

test("les machines attendues sont les non archivées : l'archive reste dans la liste mais ne compte pas", () => {
  expect(liveMachines([archivedAgent, activeAgent, { agent: null }])).toEqual([activeAgent, { agent: null }]);
});

test("garder l'historique part tout de suite ; effacer attend le nom tapé tel quel", () => {
  expect(canConfirmDeletion("vps-paris-1", "", true)).toBe(true);
  expect(canConfirmDeletion("vps-paris-1", "", false)).toBe(false);
  expect(canConfirmDeletion("vps-paris-1", "vps-paris", false)).toBe(false);
  expect(canConfirmDeletion("vps-paris-1", "VPS-PARIS-1", false)).toBe(false);
  expect(canConfirmDeletion("vps-paris-1", "vps-paris-1", false)).toBe(true);
});

test("l'option d'une machine porte son type en discret, rien sans type", () => {
  expect(machineOption(t, { id: "m", name: "web", kind: "lxc" })).toEqual({ value: "m", label: "web", hint: "machine.kind_lxc" });
  expect(machineOption(t, { id: "m", name: "web" }).hint).toBeUndefined();
});

test("le type de chaque machine se retrouve par identifiant", () => {
  expect(kindsByID([{ id: "a", kind: "vm" }]).get("a")).toBe("vm");
});
