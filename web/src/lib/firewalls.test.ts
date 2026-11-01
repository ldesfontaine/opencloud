import { expect, test } from "vitest";

import type { FirewallRule } from "../api/firewalls";
import type { Translate } from "../i18n/context";
import {
  directionLabel,
  filterRules,
  endpointLabel,
  firewallState,
  firewallStateLabel,
  firewallTone,
  gatewayDelay,
  gatewayLoss,
  gatewayTotals,
  gatewayTone,
  groupRulesByInterface,
  interfaceCardTone,
  interfaceStatusLabel,
  interfaceTone,
  readErrorLabel,
  ruleActionLabel,
  ruleActionTone,
  ruleKind,
  ruleTotals,
  shortAddress,
} from "./firewalls";

const t: Translate = (key, ...args) => (args.length === 0 ? key : `${key}(${args.join("|")})`);

function rule(over: Partial<FirewallRule> = {}): FirewallRule {
  return {
    interface: "lan",
    action: "pass",
    direction: "in",
    protocol: "TCP",
    source: "any",
    source_port: "",
    destination: "any",
    destination_port: "443",
    description: "",
    is_enabled: true,
    ...over,
  };
}

test("un pare-feu est jamais lu, en échec ou lu ; un échec garde son relevé mais se signale", () => {
  expect(firewallState({ last_read_at: null, last_error: "" })).toBe("unread");
  expect(firewallState({ last_read_at: "2026-10-28T10:00:00Z", last_error: "" })).toBe("read");
  expect(firewallState({ last_read_at: "2026-10-28T10:00:00Z", last_error: "firewall.unreachable" })).toBe("failed");
  expect(firewallState({ last_read_at: null, last_error: "firewall.unauthorized" })).toBe("failed");
  expect(firewallTone({ last_read_at: null, last_error: "" })).toBe("neutral");
  expect(firewallTone({ last_read_at: "x", last_error: "" })).toBe("ok");
  expect(firewallTone({ last_read_at: "x", last_error: "e" })).toBe("warn");
  expect(firewallStateLabel(t, { last_read_at: "x", last_error: "e" })).toBe("firewall.state_failed");
});

test("la raison du dernier relevé est une phrase du catalogue, la réponse inattendue par défaut", () => {
  expect(readErrorLabel(t, "firewall.unreachable")).toBe("firewall.unreachable");
  expect(readErrorLabel(t, "firewall.certificate")).toBe("firewall.certificate");
  expect(readErrorLabel(t, "firewall.unauthorized")).toBe("firewall.unauthorized");
  expect(readErrorLabel(t, "autre")).toBe("firewall.bad_response");
});

test("une interface active va, une interface éteinte est neutre, le reste demande un coup d'œil", () => {
  expect(interfaceTone("up")).toBe("ok");
  expect(interfaceTone("down")).toBe("neutral");
  expect(interfaceTone("no carrier")).toBe("warn");
  expect(interfaceStatusLabel(t, "up")).toBe("firewall.status_up");
  expect(interfaceStatusLabel(t, "no carrier")).toBe("firewall.status_other");
});

test("une passerelle non sondée n'a ni délai ni perte, une sondée les écrit avec leur unité", () => {
  expect(gatewayDelay({ delay_ms: null }, "fr")).toBe("—");
  expect(gatewayLoss({ loss_percent: null }, "fr")).toBe("—");
  expect(gatewayDelay({ delay_ms: 1.44 }, "fr")).toBe("1,4 ms");
  expect(gatewayLoss({ loss_percent: 100 }, "en")).toBe("100 %");
  expect(gatewayTone("online")).toBe("ok");
  expect(gatewayTone("offline")).toBe("danger");
  expect(gatewayTone("unknown")).toBe("neutral");
});

test("une règle désactivée est neutre, quelle que soit son action", () => {
  expect(ruleActionTone(rule())).toBe("ok");
  expect(ruleActionTone(rule({ action: "block" }))).toBe("danger");
  expect(ruleActionTone(rule({ action: "block", is_enabled: false }))).toBe("neutral");
  expect(ruleActionLabel(t, "pass")).toBe("firewall.rule_pass");
  expect(ruleActionLabel(t, "weird")).toBe("weird");
  expect(directionLabel(t, "in")).toBe("firewall.direction_in");
  expect(directionLabel(t, "any")).toBe("any");
});

test("une adresse et son port se lisent côte à côte, « any » quand l'adresse est vide", () => {
  expect(endpointLabel("192.168.1.0/24", "443")).toBe("192.168.1.0/24 : 443");
  expect(endpointLabel("", "")).toBe("any");
  expect(endpointLabel("(self)", "")).toBe("(self)");
});

test("une règle éteinte ne fait rien ; les autres passent, bloquent, ou sont d'une action inconnue", () => {
  expect(ruleKind(rule())).toBe("pass");
  expect(ruleKind(rule({ action: "block" }))).toBe("block");
  expect(ruleKind(rule({ action: "reject" }))).toBe("block");
  expect(ruleKind(rule({ action: "weird" }))).toBe("other");
  expect(ruleKind(rule({ action: "pass", is_enabled: false }))).toBe("off");
});

test("les totaux comptent chaque genre, une action inconnue ne compte que dans le tout", () => {
  const rules = [rule(), rule(), rule({ action: "block" }), rule({ action: "weird" }), rule({ is_enabled: false })];
  expect(ruleTotals(rules)).toEqual({ all: 5, pass: 2, block: 1, off: 1 });
  expect(ruleTotals([])).toEqual({ all: 0, pass: 0, block: 0, off: 0 });
});

test("le filtre garde un genre, et chaque mot cherché doit se trouver dans la règle", () => {
  const rules = [
    rule({ description: "Web sortant", destination_port: "443" }),
    rule({ description: "DNS", protocol: "UDP", destination_port: "53", destination: "(self)" }),
    rule({ action: "block", description: "Pas de VPS" }),
    rule({ description: "Courrier", is_enabled: false }),
  ];
  expect(filterRules(rules, "all", "")).toHaveLength(4);
  expect(filterRules(rules, "pass", "")).toHaveLength(2);
  expect(filterRules(rules, "block", "")).toHaveLength(1);
  expect(filterRules(rules, "off", "")).toHaveLength(1);
  expect(filterRules(rules, "all", "dns")).toHaveLength(1);
  expect(filterRules(rules, "all", "udp 53")).toHaveLength(1);
  expect(filterRules(rules, "all", "udp 443")).toHaveLength(0);
  expect(filterRules(rules, "pass", "courrier")).toHaveLength(0);
  expect(filterRules(rules, "all", "   ")).toHaveLength(4);
});

test("les règles se groupent par interface dans l'ordre du pare-feu ; une règle à deux interfaces est dans les deux", () => {
  const interfaces = [
    { name: "wan", role: "WAN" },
    { name: "lan", role: "LAN" },
    { name: "opt1", role: "" },
  ];
  const rules = [rule({ interface: "lan,opt1" }), rule({ interface: "wan" }), rule({ interface: "lan" })];
  const groups = groupRulesByInterface(rules, interfaces);
  expect(groups.map((group) => [group.name, group.label, group.rules.length])).toEqual([
    ["wan", "WAN", 1],
    ["lan", "LAN", 2],
    ["opt1", "opt1", 1],
  ]);
});

test("une interface que le relevé ne liste pas, et la règle flottante, ont leur groupe après les autres", () => {
  const groups = groupRulesByInterface([rule({ interface: "ovpn" }), rule({ interface: "" }), rule({ interface: "lan" })], [{ name: "lan", role: "LAN" }]);
  expect(groups.map((group) => group.name)).toEqual(["lan", "ovpn", ""]);
});

test("les passerelles se comptent : en ligne, coupées, et le reste", () => {
  expect(gatewayTotals([{ status: "online" }, { status: "online" }, { status: "offline" }, { status: "degraded" }, { status: "unknown" }])).toEqual({ all: 5, online: 2, offline: 1, other: 2 });
});

test("la carte d'une interface suit son état", () => {
  expect(interfaceCardTone("up")).toBe("up");
  expect(interfaceCardTone("down")).toBe("down");
  expect(interfaceCardTone("no carrier")).toBe("other");
});

test("l'adresse de l'API perd son schéma pour une ligne étroite", () => {
  expect(shortAddress("https://fw.lan:8443")).toBe("fw.lan:8443");
});
