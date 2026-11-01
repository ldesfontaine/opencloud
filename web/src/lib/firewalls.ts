import type { Firewall, FirewallGateway, FirewallInterface, FirewallRule, GatewayStatus } from "../api/firewalls";
import type { Translate } from "../i18n/context";
import type { Tone } from "./tone";

// L'état d'un pare-feu dans la liste : jamais lu, dernier relevé en échec, ou
// lu. Un échec garde le relevé précédent à l'écran, d'où la nuance.
type FirewallState = "unread" | "failed" | "read";

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

// L'adresse de l'API sans son schéma, pour une ligne de carte étroite.
export function shortAddress(apiURL: string): string {
  return apiURL.replace(/^https:\/\//, "");
}

// Ce qu'une règle fait, pour la compter et la filtrer : une règle éteinte ne fait
// rien, quelle que soit son action ; une action que le front ne connaît pas
// n'est ni passante ni bloquante.
type RuleKind = "pass" | "block" | "other" | "off";

export function ruleKind(rule: Pick<FirewallRule, "action" | "is_enabled">): RuleKind {
  if (!rule.is_enabled) {
    return "off";
  }
  switch (rule.action) {
    case "pass":
      return "pass";
    case "block":
    case "reject":
      return "block";
    default:
      return "other";
  }
}

// Les filtres de la liste des règles : toutes, ou un genre.
export type RuleFilter = "all" | "pass" | "block" | "off";

export const ruleFilters: readonly RuleFilter[] = ["all", "pass", "block", "off"];

export interface RuleTotals {
  all: number;
  pass: number;
  block: number;
  off: number;
}

export function ruleTotals(rules: readonly Pick<FirewallRule, "action" | "is_enabled">[]): RuleTotals {
  const totals: RuleTotals = { all: rules.length, pass: 0, block: 0, off: 0 };
  for (const rule of rules) {
    const kind = ruleKind(rule);
    if (kind === "pass" || kind === "block" || kind === "off") {
      totals[kind] += 1;
    }
  }
  return totals;
}

// Les règles qui se montrent : le genre choisi, et le texte cherché dans ce que
// l'écran affiche d'une règle (description, adresses, ports, protocole, interface).
export function filterRules<Rule extends FirewallRule>(rules: readonly Rule[], filter: RuleFilter, query: string): Rule[] {
  const words = query.toLowerCase().split(/\s+/).filter((word) => word !== "");
  return rules.filter((rule) => {
    if (filter !== "all" && ruleKind(rule) !== filter) {
      return false;
    }
    const text = [rule.description, rule.protocol, rule.source, rule.source_port, rule.destination, rule.destination_port, rule.interface, rule.action].join(" ").toLowerCase();
    return words.every((word) => text.includes(word));
  });
}

// Un groupe de règles : celles d'une interface, dans l'ordre où le pare-feu les rend.
export interface RuleGroup {
  // L'identifiant de l'interface chez le pare-feu ; vide pour les règles
  // flottantes, qui n'en nomment aucune.
  name: string;
  // Le nom que le pare-feu lui donne (LAN), l'identifiant à défaut.
  label: string;
  rules: FirewallRule[];
}

// Les règles groupées par interface : celles que la liste relève d'abord, dans
// son ordre, puis celles que les règles nomment sans qu'elle la relève (une
// interface retirée, une règle flottante). Une règle qui en nomme plusieurs
// (« lan,opt1 ») figure dans chaque groupe ; un groupe sans règle disparaît.
export function groupRulesByInterface(rules: readonly FirewallRule[], interfaces: readonly Pick<FirewallInterface, "name" | "role">[]): RuleGroup[] {
  const groups = new Map<string, RuleGroup>(interfaces.map((item) => [item.name, { name: item.name, label: item.role === "" ? item.name : item.role, rules: [] }]));
  for (const rule of rules) {
    const names = rule.interface.split(",").map((name) => name.trim()).filter((name) => name !== "");
    for (const name of names.length === 0 ? [""] : names) {
      const group = groups.get(name) ?? { name, label: name, rules: [] };
      group.rules.push(rule);
      groups.set(name, group);
    }
  }
  return [...groups.values()].filter((group) => group.rules.length > 0);
}

// Les passerelles d'un coup d'œil : combien tiennent, combien ne tiennent pas.
interface GatewayTotals {
  all: number;
  online: number;
  offline: number;
  // Dégradées ou inconnues : ni tout à fait en ligne, ni coupées.
  other: number;
}

export function gatewayTotals(gateways: readonly Pick<FirewallGateway, "status">[]): GatewayTotals {
  const totals: GatewayTotals = { all: gateways.length, online: 0, offline: 0, other: 0 };
  for (const gateway of gateways) {
    if (gateway.status === "online") {
      totals.online += 1;
    } else if (gateway.status === "offline") {
      totals.offline += 1;
    } else {
      totals.other += 1;
    }
  }
  return totals;
}

// Le ton d'une carte d'interface : « up » va, « down » s'éteint, le reste se remarque.
export function interfaceCardTone(status: string): "up" | "down" | "other" {
  return status === "up" ? "up" : status === "down" ? "down" : "other";
}
