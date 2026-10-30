import { expect, test } from "vitest";

import type { FirewallRule } from "../api/firewalls";
import type { Translate } from "../i18n/context";
import {
  directionLabel,
  endpointLabel,
  firewallState,
  firewallStateLabel,
  firewallTone,
  gatewayDelay,
  gatewayLoss,
  gatewayTone,
  interfaceStatusLabel,
  interfaceTone,
  readErrorLabel,
  ruleActionLabel,
  ruleActionTone,
  ruleInterfaces,
  rulesOfInterface,
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

test("les interfaces des règles sont dédoublonnées, une règle pouvant en nommer plusieurs", () => {
  const rules = [rule({ interface: "lan,opt1" }), rule({ interface: "wan" }), rule({ interface: "lan" }), rule({ interface: "" })];
  expect(ruleInterfaces(rules)).toEqual(["lan", "opt1", "wan"]);
  expect(rulesOfInterface(rules, "opt1")).toHaveLength(1);
  expect(rulesOfInterface(rules, "lan")).toHaveLength(2);
  expect(rulesOfInterface(rules, "")).toHaveLength(4);
});

test("l'adresse de l'API perd son schéma pour une ligne étroite", () => {
  expect(shortAddress("https://fw.lan:8443")).toBe("fw.lan:8443");
});
