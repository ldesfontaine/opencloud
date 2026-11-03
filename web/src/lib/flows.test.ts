import { expect, test } from "vitest";

import type { FlowObject, FlowRule, FlowZone } from "../api/firewalls";
import type { Translate } from "../i18n/context";
import { cellOf, cellSummary, flowLine, objectText, serviceText, zoneName } from "./flows";

const t: Translate = (key, ...args) => (args.length === 0 ? key : `${key}(${args.join("|")})`);

const zones: FlowZone[] = [
  { id: "self", label: "", kind: "self", network: "", is_up: true },
  { id: "lan", label: "LAN", kind: "network", network: "192.168.1.0/24", is_up: true },
  { id: "wan", label: "", kind: "internet", network: "", is_up: true },
  { id: "opt3", label: "", kind: "network", network: "", is_up: false },
];

const objects: FlowObject[] = [
  { id: "any", kind: "any", label: "any", zone: "*", is_negated: false },
  { id: "self", kind: "self", label: "(self)", zone: "self", is_negated: false },
  { id: "zone:lan", kind: "zone", label: "lan", zone: "lan", is_negated: false },
  { id: "host:192.168.1.20", kind: "host", label: "192.168.1.20", zone: "lan", is_negated: false },
  { id: "!named:Servers", kind: "named", label: "Servers", zone: "", is_negated: true },
];

function rule(over: Partial<FlowRule> = {}): FlowRule {
  return {
    ref: "r1",
    from: { zone: "lan", object: "host:192.168.1.20", except: "" },
    to: { zone: "self", object: "self", except: "" },
    protocol: "TCP",
    port: "22",
    source_port: "",
    why: "SSH",
    effect: "allow",
    is_enabled: true,
    ...over,
  };
}

test("une zone se nomme par son libellé, sinon par son genre, sinon par son identifiant", () => {
  expect(zoneName(t, zones[1]!)).toBe("LAN");
  expect(zoneName(t, zones[0]!)).toBe("firewall.zone_self");
  expect(zoneName(t, zones[2]!)).toBe("firewall.zone_internet");
  expect(zoneName(t, zones[3]!)).toBe("opt3");
});

test("un objet se dit selon son genre et son côté", () => {
  expect(objectText(t, objects[0], zones, "from")).toBe("firewall.flow_any_from");
  expect(objectText(t, objects[0], zones, "to")).toBe("firewall.flow_any_to");
  expect(objectText(t, objects[1], zones, "to")).toBe("firewall.flow_self");
  expect(objectText(t, objects[2], zones, "from")).toBe("firewall.flow_zone(LAN)");
  expect(objectText(t, objects[3], zones, "from")).toBe("192.168.1.20");
});

test("un objet nié se dit « tout sauf »", () => {
  expect(objectText(t, objects[4], zones, "to")).toBe("firewall.flow_not(Servers)");
});

test("un objet inconnu se montre par un point d'interrogation, jamais par un blanc", () => {
  expect(objectText(t, undefined, zones, "to")).toBe("?");
});

test("le quoi : tout, un protocole, un port seul, les deux, et un port de source", () => {
  expect(serviceText(t, { protocol: "", port: "", source_port: "" })).toBe("firewall.flow_everything");
  expect(serviceText(t, { protocol: "UDP", port: "", source_port: "" })).toBe("UDP");
  expect(serviceText(t, { protocol: "", port: "53", source_port: "" })).toBe("firewall.flow_port(53)");
  expect(serviceText(t, { protocol: "TCP", port: "443", source_port: "" })).toBe("TCP 443");
  expect(serviceText(t, { protocol: "TCP", port: "443", source_port: "1024" })).toBe("TCP 443 firewall.flow_from_port(1024)");
});

test("une règle se lit « de → vers : quoi », avec son pourquoi", () => {
  expect(flowLine(t, rule(), { objects, zones })).toEqual({ from: "192.168.1.20", to: "firewall.flow_self", what: "TCP 22", why: "SSH" });
});

test("une case absente est refusée ; une case garnie compte ses lignes", () => {
  const cell = { from: "lan", to: "self", allowed: [rule()], denied: [] };
  expect(cellOf({ cells: [cell] }, "lan", "self")).toBe(cell);
  expect(cellOf({ cells: [cell] }, "self", "lan")).toBeUndefined();
  expect(cellSummary(t, undefined)).toBe("firewall.flow_refused");
  expect(cellSummary(t, { from: "a", to: "b", allowed: [], denied: [] })).toBe("firewall.flow_refused");
  expect(cellSummary(t, cell)).toBe("firewall.flow_cell_summary(1|0)");
});
