import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import { capabilitiesOf, capabilityLabelKey, describeAccount, describeCapability, describeUnavailable, emptyMachineForm, machineSeen, machinesSubtitle, rightsFormOf, toggleRight, unavailableRights, unitDropIn, unitWritePaths } from "./machines";

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

test("unitWritePaths : les droits de sauvegarde et de déploiement écrivent hors de l'état", () => {
  expect(unitWritePaths([{ name: "docker.deploy", paths: [] }])).toEqual(["/srv/workspace"]);
  expect(unitWritePaths([{ name: "docker.update", paths: [] }, { name: "backup.receive", paths: ["/srv/backups"] }])).toEqual(["/srv/backups", "/srv/workspace"]);
  expect(unitWritePaths([{ name: "docker.deploy", paths: [] }, { name: "backup.receive", paths: ["/srv/workspace"] }])).toEqual(["/srv/workspace"]);
  expect(unitWritePaths([{ name: "agent.update", paths: [] }, { name: "backup.receive", paths: ["/srv/backups"] }])).toEqual(["/srv/backups"]);
  expect(unitWritePaths([{ name: "files.write", paths: ["/srv/a"] }])).toEqual([]);
  expect(unitWritePaths([])).toEqual([]);
});

test("unitDropIn : le fichier à poser dans le dossier drop-in de l'unité de l'agent", () => {
  expect(unitDropIn(["/srv/backups"])).toBe("[Service]\nReadWritePaths=/srv/backups");
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
