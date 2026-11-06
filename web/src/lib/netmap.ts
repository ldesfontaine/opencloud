import type { MapBox, MapEntry, MapLink, MapService, MapTunnel, NetworkMap, ServiceState } from "../api/network";
import type { Translate } from "../i18n/context";
import { freshnessText } from "./links";
import type { Tone } from "./tone";

// Combien de puces une machine montre avant « et n autres ».
const chipsShown = 6;

// Toutes les machines de la carte : le dehors, puis les couloirs de la maison.
export function allBoxes(map: NetworkMap): MapBox[] {
  return [...map.outside, ...map.home.flatMap((lane) => lane.boxes)];
}

export function isHost(box: MapBox): boolean {
  return box.guest_count > 0;
}

// Les invités d'un hôte, où qu'ils soient dans la carte.
export function guestsOf(map: NetworkMap, hostID: string): MapBox[] {
  return allBoxes(map).filter((box) => box.host_id === hostID);
}

// Un service qui demande un geste : en défaut, ou à moitié là.
function isTrouble(state: ServiceState): boolean {
  return state === "failed" || state === "degraded";
}

// La machine et ses invités : de quoi résumer un hôte.
function machinesIn(map: NetworkMap, box: MapBox): MapBox[] {
  return [box, ...guestsOf(map, box.id)];
}

export function serviceCount(map: NetworkMap, box: MapBox): number {
  return machinesIn(map, box).reduce((sum, machine) => sum + machine.services.length, 0);
}

// Le pire état d'une machine et de ses invités : « en échec » avant « dégradé » ;
// null quand rien ne demande un geste.
export function worstState(map: NetworkMap, box: MapBox): "failed" | "degraded" | null {
  const states = machinesIn(map, box).flatMap((machine) => machine.services.map((service) => service.state));
  if (states.includes("failed")) {
    return "failed";
  }
  return states.includes("degraded") ? "degraded" : null;
}

// Les hôtes repliés : ceux que le serveur présente repliés, sauf ce que
// l'opérateur a choisi à la main, qui l'emporte et que le rafraîchissement
// n'efface pas.
export function foldedHosts(map: NetworkMap, chosen: ReadonlyMap<string, boolean>): Set<string> {
  const folded = new Set(allBoxes(map).filter((box) => box.folded).map((box) => box.id));
  for (const [id, isFolded] of chosen) {
    if (isFolded) {
      folded.add(id);
    } else {
      folded.delete(id);
    }
  }
  return folded;
}

// Un invité d'un hôte replié n'est pas dessiné : l'hôte parle pour lui.
export function isHidden(box: MapBox, folded: ReadonlySet<string>): boolean {
  return box.host_id !== "" && folded.has(box.host_id);
}

// Les zones des invités d'un hôte, dans l'ordre de la carte : ce que dit un
// hôte replié de ce qu'il cache.
export function guestZones(map: NetworkMap, hostID: string): string[] {
  const zones = new Set(guestsOf(map, hostID).map((guest) => guest.zone_id));
  return map.home.map((lane) => lane.zone_id).filter((zone) => zones.has(zone));
}

// La carte de l'hôte, ou ses invités, en évidence : celui qu'on survole ou
// qu'on a choisi.
export function isLit(box: MapBox, lit: string | null): boolean {
  return lit !== null && (box.id === lit || box.host_id === lit);
}

// Où s'accroche un trait qui vise une machine : sa carte, ou celle de son hôte
// quand il est replié.
export function boxAnchorOf(map: NetworkMap, boxID: string, folded: ReadonlySet<string>): string | null {
  const found = allBoxes(map).find((box) => box.id === boxID);
  if (found === undefined) {
    return null;
  }
  return isHidden(found, folded) ? found.host_id : found.id;
}

// L'ancre d'un service dans le dessin : sa puce, ou, quand la machine est dans
// un hôte replié, la carte de l'hôte. Une ancre inconnue rend null.
export function anchorOf(map: NetworkMap, serviceID: string, folded: ReadonlySet<string>): string | null {
  const machine = allBoxes(map).find((box) => box.services.some((service) => service.id === serviceID));
  if (machine === undefined) {
    return null;
  }
  return isHidden(machine, folded) ? machine.host_id : serviceID;
}

interface DrawnLink {
  link: MapLink;
  from: string;
  to: string;
}

// Les liens à tracer : ceux dont les deux bouts ont une ancre, un seul trait
// par paire d'ancres et de preuve quand des services repliés se rejoignent
// (le plus récent par la fraîcheur : un lien qui n'est pas ancien l'emporte).
export function drawnLinks(map: NetworkMap, folded: ReadonlySet<string>): DrawnLink[] {
  const byPair = new Map<string, DrawnLink>();
  for (const link of map.links) {
    const from = anchorOf(map, link.from, folded);
    const to = anchorOf(map, link.to, folded);
    if (from === null || to === null || from === to) {
      continue;
    }
    const key = `${from}>${to}>${link.proof}>${link.nature}`;
    const known = byPair.get(key);
    if (known === undefined || (known.link.stale && !link.stale)) {
      byPair.set(key, { link, from, to });
    }
  }
  return [...byPair.values()];
}

// Le texte d'une liste de ports : « 443, 80 ».
export function portsText(ports: readonly number[]): string {
  return ports.join(", ");
}

// Les puces montrées d'une machine : les premières, les services en défaut
// d'abord ; le reste se compte.
export function shownServices(box: MapBox, limit = chipsShown): { shown: MapService[]; hidden: number } {
  const ordered = [...box.services].sort((a, b) => Number(isTrouble(b.state)) - Number(isTrouble(a.state)));
  return { shown: ordered.slice(0, limit), hidden: Math.max(0, ordered.length - limit) };
}

// L'adresse où mène une puce : le service déclaré, sinon le conteneur ; la
// fiche de la machine quand la puce n'a ni l'un ni l'autre (une unité nue).
export function chipTarget(service: MapService, box: MapBox): string {
  if (service.service_id !== "") {
    return `/services/annuaire/${service.service_id}`;
  }
  if (service.container_id !== "") {
    return `/services/conteneurs/${service.container_id}`;
  }
  return `/machines/${box.machine_id}`;
}

// L'adresse d'une machine de la carte ; vide pour un invité sans agent.
export function boxTarget(box: MapBox): string {
  return box.machine_id === "" ? "" : `/machines/${box.machine_id}`;
}

// Une puce et la machine qui la porte : de quoi nommer un bout de lien.
export interface ServiceSpot {
  service: MapService;
  box: MapBox;
}

export function serviceIndex(map: NetworkMap): Map<string, ServiceSpot> {
  const index = new Map<string, ServiceSpot>();
  for (const machine of allBoxes(map)) {
    for (const service of machine.services) {
      index.set(service.id, { service, box: machine });
    }
  }
  return index;
}

// Un rectangle mesuré, dans le repère du dessin.
export interface Rect {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

interface Point {
  x: number;
  y: number;
}

const minReach = 28;

// Le tracé d'un lien entre deux ancres : une courbe qui sort du côté qui fait
// face à l'autre ancre, de gauche à droite quand elles sont côte à côte, de
// haut en bas quand elles se recouvrent en largeur. Le tracé est le même quel
// que soit le sens du lien : seul le marqueur dit le sens. Avec `fromAbove`,
// une ancre entièrement au-dessus de l'autre sort par le bas et arrive par le
// haut, même si elles ne se recouvrent pas en largeur : c'est le trait d'un
// tunnel quand Ailleurs est posé au-dessus de Chez vous.
export function anchorPath(from: Rect, to: Rect, fromAbove = false): { path: string; middle: Point } {
  let start: Point;
  let end: Point;
  let reachX = 0;
  let reachY = 0;
  const stacked = fromAbove && from.bottom <= to.top;
  if (!stacked && from.right <= to.left) {
    start = { x: from.right, y: (from.top + from.bottom) / 2 };
    end = { x: to.left, y: (to.top + to.bottom) / 2 };
    reachX = Math.max(minReach, (end.x - start.x) / 2);
  } else if (!stacked && to.right <= from.left) {
    start = { x: from.left, y: (from.top + from.bottom) / 2 };
    end = { x: to.right, y: (to.top + to.bottom) / 2 };
    reachX = -Math.max(minReach, (start.x - end.x) / 2);
  } else if (from.bottom <= to.top) {
    start = { x: (from.left + from.right) / 2, y: from.bottom };
    end = { x: (to.left + to.right) / 2, y: to.top };
    reachY = Math.max(minReach, (end.y - start.y) / 2);
  } else {
    start = { x: (from.left + from.right) / 2, y: from.top };
    end = { x: (to.left + to.right) / 2, y: to.bottom };
    reachY = -Math.max(minReach, (start.y - end.y) / 2);
  }
  const path = `M${round(start.x)} ${round(start.y)} C${round(start.x + reachX)} ${round(start.y + reachY)} ${round(end.x - reachX)} ${round(end.y - reachY)} ${round(end.x)} ${round(end.y)}`;
  return { path, middle: { x: round((start.x + end.x) / 2), y: round((start.y + end.y) / 2) } };
}

function round(value: number): number {
  return Math.round(value * 10) / 10;
}

export function interfaceAnchorID(firewallID: string, name: string): string {
  return `iface:${firewallID}:${name}`;
}

// La carte d'une entrée d'Internet, dans la région Internet.
export function entryAnchorID(serviceID: string): string {
  return `entry:${serviceID}`;
}

export const homeAnchorID = "home";

// Le mot du badge d'une machine : son type, avec le numéro d'invité quand
// l'hôte en donne un (« VM 5010 »).
export function badgeText(t: Translate, box: Pick<MapBox, "kind" | "vmid">): string {
  const kind = t(`machine.kind_${box.kind}`);
  return box.vmid > 0 ? `${kind} ${box.vmid}` : kind;
}

// Le ton de la pastille d'un service ; un état inconnu ou absent reste neutre,
// jamais vert ni rouge : ce n'est pas un verdict.
export function stateTone(state: ServiceState): Tone {
  switch (state) {
    case "running":
      return "ok";
    case "degraded":
      return "warn";
    case "failed":
      return "danger";
    default:
      return "neutral";
  }
}

// Ce que l'info-bulle d'un lien dit : les deux bouts, la preuve en mots, d'où
// elle vient, le port, la fraîcheur. Jamais une valeur de configuration.
export function linkTip(t: Translate, link: MapLink, spots: ReadonlyMap<string, ServiceSpot>, now: number): { title: string; lines: string[] } {
  const name = (id: string) => {
    const spot = spots.get(id);
    return spot === undefined ? id : `${spot.service.label} (${spot.box.name})`;
  };
  const lines = [`${t(`link.proof_${link.proof}`)} · ${t(`link.signal_${link.signal}`)}`, t(`link.nature_${link.nature}`)];
  if (link.port > 0) {
    lines.push(t("link.port", link.port));
  }
  lines.push(link.proposed ? t("network.map_link_proposed") : freshnessText(t, { ...link, dismissed: false }, now));
  if (link.stale) {
    lines.push(t("link.stale"));
  }
  return { title: `${name(link.from)} → ${name(link.to)}`, lines };
}

// Tout ce que le dessin trace, par ordre de pose : les entrées d'Internet, les
// liens, les tunnels par-dessus. Aucun trait ne part d'une interface du
// pare-feu vers une machine : la zone se lit au couloir.
export type Edge =
  | { key: string; kind: "entry"; from: string; to: string; entry: MapEntry }
  | { key: string; kind: "link"; from: string; to: string; link: MapLink }
  | { key: string; kind: "tunnel"; from: string; to: string; tunnel: MapTunnel };

export function drawnEdges(map: NetworkMap, folded: ReadonlySet<string>): Edge[] {
  const edges: Edge[] = [];
  for (const entry of map.entries) {
    const to = anchorOf(map, entry.to, folded);
    if (to !== null) {
      edges.push({ key: `entry:${entry.to}`, kind: "entry", from: entryAnchorID(entry.to), to, entry });
    }
  }
  for (const drawn of drawnLinks(map, folded)) {
    edges.push({ key: `link:${drawn.link.id}`, kind: "link", from: drawn.from, to: drawn.to, link: drawn.link });
  }
  for (const tunnel of map.tunnels) {
    const from = boxAnchorOf(map, tunnel.outside, folded);
    const to = tunnel.firewall === "" ? homeAnchorID : interfaceAnchorID(tunnel.firewall, tunnel.interface);
    if (from !== null) {
      edges.push({ key: `tunnel:${tunnel.id}`, kind: "tunnel", from, to, tunnel });
    }
  }
  return edges;
}
