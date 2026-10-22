import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import { capabilitiesOf, capabilityLabelKey, describeCapability, machineSeen, machinesSubtitle, rightsFormOf, toggleRight, unitDropIn, unitWritePaths } from "./machines";

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

test("unitWritePaths : seul le droit de recevoir des sauvegardes écrit hors de l'état", () => {
  expect(unitWritePaths([{ name: "agent.update", paths: [] }, { name: "backup.receive", paths: ["/srv/backups"] }])).toEqual(["/srv/backups"]);
  expect(unitWritePaths([{ name: "files.write", paths: ["/srv/a"] }])).toEqual([]);
  expect(unitWritePaths([])).toEqual([]);
});

test("unitDropIn : le fichier à poser dans le dossier drop-in de l'unité de l'agent", () => {
  expect(unitDropIn(["/srv/backups"])).toBe("[Service]\nReadWritePaths=/srv/backups");
});
