import type { FirewallFlowsResponse, FlowCell, FlowObject, FlowRule, FlowZone } from "../api/firewalls";
import type { Translate } from "../i18n/context";

// La grille « qui parle à qui » se lit dans le modèle des échanges, jamais dans
// le vocabulaire d'un constructeur : ce fichier met en mots ce que le serveur a
// traduit (zones, objets, règles « de → vers : quoi, pourquoi »).

// Le nom d'une zone : celui que le pare-feu lui donne, sinon celui que
// l'écran donne à son genre (le pare-feu lui-même, Internet).
export function zoneName(t: Translate, zone: Pick<FlowZone, "id" | "label" | "kind">): string {
  if (zone.label !== "") {
    return zone.label;
  }
  switch (zone.kind) {
    case "self":
      return t("firewall.zone_self");
    case "internet":
      return t("firewall.zone_internet");
    default:
      return zone.id;
  }
}

export function zoneOf(flows: Pick<FirewallFlowsResponse, "zones">, id: string): FlowZone | undefined {
  return flows.zones.find((zone) => zone.id === id);
}

// La case d'une ligne et d'une colonne ; absente : rien n'est écrit, donc refusé.
export function cellOf(flows: Pick<FirewallFlowsResponse, "cells">, from: string, to: string): FlowCell | undefined {
  return flows.cells.find((cell) => cell.from === from && cell.to === to);
}

// Un bout de règle en mots. « any » se dit « tous » côté départ, « tout » côté
// arrivée ; une zone entière, « tout LAN ».
export function objectText(t: Translate, object: FlowObject | undefined, zones: readonly FlowZone[], side: "from" | "to"): string {
  if (object === undefined) {
    return "?";
  }
  const base = baseObjectText(t, object, zones, side);
  return object.is_negated ? t("firewall.flow_not", base) : base;
}

function baseObjectText(t: Translate, object: FlowObject, zones: readonly FlowZone[], side: "from" | "to"): string {
  switch (object.kind) {
    case "any":
      return t(side === "from" ? "firewall.flow_any_from" : "firewall.flow_any_to");
    case "self":
      return t("firewall.flow_self");
    case "zone": {
      const zone = zones.find((candidate) => candidate.id === object.zone);
      return t("firewall.flow_zone", zone === undefined ? object.label : zoneName(t, zone));
    }
    default:
      // Une adresse, un bloc, un groupe nommé : tels que le pare-feu les écrit.
      return object.label;
  }
}

// Le « quoi » : protocole et port, « tout » quand la règle n'en dit aucun.
export function serviceText(t: Translate, rule: Pick<FlowRule, "protocol" | "port" | "source_port">): string {
  let what: string;
  if (rule.protocol === "" && rule.port === "") {
    what = t("firewall.flow_everything");
  } else if (rule.port === "") {
    what = rule.protocol;
  } else if (rule.protocol === "") {
    what = t("firewall.flow_port", rule.port);
  } else {
    what = `${rule.protocol} ${rule.port}`;
  }
  return rule.source_port === "" ? what : `${what} ${t("firewall.flow_from_port", rule.source_port)}`;
}

interface FlowLine {
  from: string;
  to: string;
  what: string;
  why: string;
}

// Une règle en une ligne : « de → vers : quoi », et pourquoi en dessous.
export function flowLine(t: Translate, rule: FlowRule, flows: Pick<FirewallFlowsResponse, "objects" | "zones">): FlowLine {
  const object = (id: string) => flows.objects.find((candidate) => candidate.id === id);
  return {
    from: objectText(t, object(rule.from.object), flows.zones, "from"),
    to: objectText(t, object(rule.to.object), flows.zones, "to"),
    what: serviceText(t, rule),
    why: rule.why,
  };
}

// Ce qu'une case dit en une phrase, pour la lire sans la voir : le nombre de
// lignes qui passent, ou « refusé ».
export function cellSummary(t: Translate, cell: FlowCell | undefined): string {
  if (cell === undefined || (cell.allowed.length === 0 && cell.denied.length === 0)) {
    return t("firewall.flow_refused");
  }
  return t("firewall.flow_cell_summary", cell.allowed.length, cell.denied.length);
}
