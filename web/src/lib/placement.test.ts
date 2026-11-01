import { expect, test } from "vitest";

import type { Machine, MachineHost } from "../api/machines";
import { hostCandidates, hostOptions, isPlacementModified, kindIDs, placementOf } from "./placement";

function machine(id: string, host: MachineHost | null = null, kindOverride: Machine["kind_override"] = ""): Machine {
  return { id, name: id, host, kind_override: kindOverride } as Machine;
}

function hostedBy(id: string, source: MachineHost["source"]): MachineHost {
  return { machine_id: id, machine_name: id, node: "", source };
}

test("les types sont ceux du serveur, « inconnu » en dernier", () => {
  expect(kindIDs).toEqual(["physical", "vm", "lxc", "vps", "unknown"]);
});

test("la correction d'une machine : son type, et l'hôte seulement s'il est désigné à la main", () => {
  expect(placementOf(machine("a", hostedBy("pve", "override"), "vps"))).toEqual({ kindOverride: "vps", hostOverride: "pve" });
  // Un hôte que Proxmox rapproche n'est pas une correction : on ne le rejoue pas.
  expect(placementOf(machine("a", hostedBy("pve", "guest")))).toEqual({ kindOverride: "", hostOverride: "" });
});

test("isPlacementModified compare les deux champs", () => {
  const initial = { kindOverride: "", hostOverride: "" } as const;
  expect(isPlacementModified(initial, initial)).toBe(false);
  expect(isPlacementModified({ ...initial, kindOverride: "lxc" }, initial)).toBe(true);
  expect(isPlacementModified({ ...initial, hostOverride: "pve" }, initial)).toBe(true);
});

test("on ne propose pas comme hôte la machine elle-même ni celles qu'elle porte, de près ou de loin", () => {
  const all = [machine("a"), machine("b", hostedBy("a", "override")), machine("c", hostedBy("b", "override")), machine("d")];
  expect(hostCandidates(all[0] as Machine, all).map((candidate) => candidate.id)).toEqual(["d"]);
  expect(hostCandidates(all[3] as Machine, all).map((candidate) => candidate.id)).toEqual(["a", "b", "c"]);
});

test("une boucle déjà là ne fait pas tourner le calcul", () => {
  const all = [machine("a", hostedBy("b", "override")), machine("b", hostedBy("a", "override"))];
  expect(hostCandidates(all[0] as Machine, all).map((candidate) => candidate.id)).toEqual([]);
});

test("hostOptions met « aucun » d'abord", () => {
  expect(hostOptions((key) => key, "Aucun", [machine("a")])).toEqual([
    { value: "", label: "Aucun" },
    { value: "a", label: "a" },
  ]);
});
