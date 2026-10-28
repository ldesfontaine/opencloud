import type { Firewall, FirewallGateway, FirewallRule, GatewayStatus } from "../api/firewalls";
import type { Translate } from "../i18n/context";
import type { Tone } from "./tone";

// L'état d'un pare-feu dans la liste : jamais lu, dernier relevé en échec, ou
// lu. Un échec garde le relevé précédent à l'écran, d'où la nuance.
export type FirewallState = "unread" | "failed" | "read";

export function firewallState(firewall: Pick<Firewall, "last_read_at" | "last_error">): FirewallState {
  if (firewall.last_error !== "") {
    return "failed";
  }
  return firewall.last_read_at === null ? "unread" : "read";
}

const stateTones: Record<FirewallState, Tone> = { unread: "neutral", failed: "warn", read: "ok" };

export function firewallTone(firewall: Pick<Firewall, "last_read_at" | "last_error">): Tone {
  return stateTones[firewallState(firewall)];
}

export function firewallStateLabel(t: Translate, firewall: Pick<Firewall, "last_read_at" | "last_error">): string {
  return t(`firewall.state_${firewallState(firewall)}`);
}

// La phrase du dernier relevé qui a échoué : la clé du refus, traduite. Une clé
// que le front ne connaît pas montre la réponse inattendue.
export function readErrorLabel(t: Translate, key: string): string {
  switch (key) {
    case "firewall.unreachable":
      return t("firewall.unreachable");
    case "firewall.certificate":
      return t("firewall.certificate");
    case "firewall.unauthorized":
      return t("firewall.unauthorized");
    default:
      return t("firewall.bad_response");
  }
}

// L'état d'une interface : « up » va, « down » est neutre, le reste (« no
// carrier »…) demande un coup d'œil.
export function interfaceTone(status: string): Tone {
  if (status === "up") {
    return "ok";
  }
  return status === "down" ? "neutral" : "warn";
}

export function interfaceStatusLabel(t: Translate, status: string): string {
  switch (status) {
    case "up":
      return t("firewall.status_up");
    case "down":
      return t("firewall.status_down");
    default:
      return t("firewall.status_other");
  }
}

const gatewayTones: Record<GatewayStatus, Tone> = { online: "ok", offline: "danger", degraded: "warn", unknown: "neutral" };

export function gatewayTone(status: GatewayStatus): Tone {
  return gatewayTones[status];
}

export function gatewayStatusLabel(t: Translate, status: GatewayStatus): string {
  return t(`firewall.gateway_${status}`);
}

// Le délai et la perte d'une passerelle sondée : « 1,4 ms » ; un tiret quand le
// pare-feu ne la sonde pas.
export function gatewayDelay(gateway: Pick<FirewallGateway, "delay_ms">, language: string): string {
  return gateway.delay_ms === null ? "—" : `${decimal(language, gateway.delay_ms)} ms`;
}

export function gatewayLoss(gateway: Pick<FirewallGateway, "loss_percent">, language: string): string {
  return gateway.loss_percent === null ? "—" : `${decimal(language, gateway.loss_percent)} %`;
}

function decimal(language: string, value: number): string {
  return new Intl.NumberFormat(language, { maximumFractionDigits: 1 }).format(value);
}

// L'action d'une règle : le mot du catalogue pour les trois que OPNsense
// connaît, le mot du pare-feu tel quel pour un autre.
export function ruleActionLabel(t: Translate, action: string): string {
  switch (action) {
    case "pass":
      return t("firewall.rule_pass");
    case "block":
      return t("firewall.rule_block");
    case "reject":
      return t("firewall.rule_reject");
    default:
      return action;
  }
}

export function ruleActionTone(rule: Pick<FirewallRule, "action" | "is_enabled">): Tone {
  if (!rule.is_enabled) {
    return "neutral";
  }
  switch (rule.action) {
    case "pass":
      return "ok";
    case "block":
    case "reject":
      return "danger";
    default:
      return "neutral";
  }
}

export function directionLabel(t: Translate, direction: string): string {
  switch (direction) {
    case "in":
      return t("firewall.direction_in");
    case "out":
      return t("firewall.direction_out");
    default:
      return direction;
  }
}

// Une adresse suivie de son port : « 10.0.0.2:443 » se lit mal avec une plage
// ou une IPv6, on les garde côte à côte et le port en discret.
export function endpointLabel(address: string, port: string): string {
  const shown = address === "" ? "any" : address;
  return port === "" ? shown : `${shown} : ${port}`;
}

// Les interfaces que les règles nomment (une règle peut en nommer plusieurs,
// séparées par une virgule), sans doublon, dans l'ordre alphabétique.
export function ruleInterfaces(rules: readonly Pick<FirewallRule, "interface">[]): string[] {
  const names = new Set<string>();
  for (const rule of rules) {
    for (const name of rule.interface.split(",")) {
      if (name.trim() !== "") {
        names.add(name.trim());
      }
    }
  }
  return [...names].sort((a, b) => a.localeCompare(b));
}

// Les règles d'une interface ; « » les garde toutes.
export function rulesOfInterface<Rule extends Pick<FirewallRule, "interface">>(rules: readonly Rule[], name: string): Rule[] {
  if (name === "") {
    return [...rules];
  }
  return rules.filter((rule) => rule.interface.split(",").some((candidate) => candidate.trim() === name));
}

// L'adresse de l'API sans son schéma, pour une ligne de carte étroite.
export function shortAddress(apiURL: string): string {
  return apiURL.replace(/^https:\/\//, "");
}
