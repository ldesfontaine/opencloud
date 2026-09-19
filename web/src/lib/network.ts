import type { Edge, Group, NetworkResponse, OverviewNetworkResponse, Service } from "../api/types";
import { matchesFilter, type Filter } from "./services";

// Le placement du graphe de la planche « Machine · Réseau » : le serveur a
// dit les nœuds, les groupes et les arêtes ; ici on décide où. Trois
// colonnes : Internet ; ce qu'Internet atteint, services publiés et
// groupes qui en abritent un ; puis le reste. Ainsi une route publique ne
// traverse jamais un nœud, et une dépendance ne traverse qu'un écart.
// Tout est en pixels du graphe, avant zoom.

export const nodeWidth = 184;
export const nodeHeight = 54;
const columnGap = 72;
const rowGap = 24;
const groupGap = 24;
const groupPadding = { top: 28, right: 44, bottom: 14, left: 14 };
const margin = 24;
const cornerRadius = 8;
// La boucle d'une arête entre deux nœuds de la même colonne passe à droite.
const loopOffset = 22;

export const internetID = "internet";

export interface PlacedNode {
  id: string;
  x: number;
  y: number;
  service: Service | null;
  public: boolean;
}

export interface PlacedGroup extends Group {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface PlacedEdge {
  edge: Edge;
  path: string;
  // Où écrire le port d'une route publique.
  labelX: number;
  labelY: number;
}

export interface Layout {
  width: number;
  height: number;
  nodes: PlacedNode[];
  groups: PlacedGroup[];
  edges: PlacedEdge[];
}

interface Box {
  x: number;
  y: number;
}

// Une unité d'une colonne : un service hors groupe, ou un groupe avec ses
// membres, publiés d'abord.
interface Unit {
  group: Group | null;
  services: Service[];
  public: boolean;
}

interface Column {
  nodes: PlacedNode[];
  groups: PlacedGroup[];
  height: number;
}

export function layoutNetwork(response: NetworkResponse, filter: Filter): Layout {
  const services = response.services.filter((service) => matchesFilter(service, filter));
  const visible = new Set(services.map((service) => service.id));
  const published = new Set(response.edges.filter((edge) => edge.kind === "public" && visible.has(edge.to)).map((edge) => edge.to));
  const units = unitsOf(services, response.groups, published);
  const reached = [...units.filter((unit) => unit.public && unit.group === null), ...units.filter((unit) => unit.public && unit.group !== null)];
  const rest = [...units.filter((unit) => !unit.public && unit.group !== null), ...units.filter((unit) => !unit.public && unit.group === null)];

  const internet: PlacedNode = { id: internetID, x: margin, y: margin, service: null, public: false };
  const columns: Column[] = [];
  let x = margin + nodeWidth + columnGap;
  for (const column of [reached, rest]) {
    if (column.length === 0) {
      continue;
    }
    columns.push(placeColumn(column, x, published));
    x += groupPadding.left + nodeWidth + groupPadding.right + columnGap;
  }
  const tallest = Math.max(nodeHeight, ...columns.map((column) => column.height));
  for (const column of columns) {
    shiftColumn(column, (tallest - column.height) / 2);
  }
  const first = columns[0];
  internet.y = margin + (first === undefined ? (tallest - nodeHeight) / 2 : first.height / 2 - nodeHeight / 2 + (tallest - first.height) / 2);

  const nodes = [internet, ...columns.flatMap((column) => column.nodes)];
  const groups = columns.flatMap((column) => column.groups);
  const positions = new Map(nodes.map((node) => [node.id, node]));
  const edges: PlacedEdge[] = [];
  for (const edge of response.edges) {
    const from = positions.get(edge.from);
    const to = positions.get(edge.to);
    if (from && to) {
      edges.push(placeEdge(edge, from, to));
    }
  }
  return { width: x - columnGap + margin, height: margin + tallest + margin, nodes, groups, edges };
}

function unitsOf(services: Service[], groups: Group[], published: Set<string>): Unit[] {
  const byID = new Map(services.map((service) => [service.id, service]));
  const grouped = new Set<string>();
  const units: Unit[] = [];
  for (const group of groups) {
    const members = publishedFirst(group.members.map((id) => byID.get(id)).filter((service): service is Service => service !== undefined), published);
    if (members.length === 0) {
      continue;
    }
    for (const member of members) {
      grouped.add(member.id);
    }
    units.push({ group, services: members, public: members.some((member) => published.has(member.id)) });
  }
  for (const service of services) {
    if (!grouped.has(service.id)) {
      units.push({ group: null, services: [service], public: published.has(service.id) });
    }
  }
  return units;
}

function publishedFirst(services: Service[], published: Set<string>): Service[] {
  return [...services.filter((service) => published.has(service.id)), ...services.filter((service) => !published.has(service.id))];
}

// Empile les unités d'une colonne ; les nœuds hors groupe s'alignent sur
// les membres des groupes.
function placeColumn(units: Unit[], x: number, published: Set<string>): Column {
  const column: Column = { nodes: [], groups: [], height: 0 };
  let y = margin;
  for (const unit of units) {
    if (unit.group === null) {
      const service = unit.services[0] as Service;
      column.nodes.push({ id: service.id, x: x + groupPadding.left, y, service, public: published.has(service.id) });
      y += nodeHeight + groupGap;
      continue;
    }
    const height = groupPadding.top + unit.services.length * nodeHeight + (unit.services.length - 1) * rowGap + groupPadding.bottom;
    column.groups.push({ ...unit.group, x, y, width: groupPadding.left + nodeWidth + groupPadding.right, height });
    let inner = y + groupPadding.top;
    for (const service of unit.services) {
      column.nodes.push({ id: service.id, x: x + groupPadding.left, y: inner, service, public: published.has(service.id) });
      inner += nodeHeight + rowGap;
    }
    y += height + groupGap;
  }
  column.height = y - groupGap - margin;
  return column;
}

function shiftColumn(column: Column, shift: number): void {
  if (shift <= 0) {
    return;
  }
  for (const node of column.nodes) {
    node.y += shift;
  }
  for (const group of column.groups) {
    group.y += shift;
  }
}

// Une arête orthogonale à coins arrondis, comme la planche : elle sort par
// le côté droit et entre par le côté gauche ; entre deux nœuds de la même
// colonne, elle boucle à droite.
function placeEdge(edge: Edge, from: Box, to: Box): PlacedEdge {
  const fromCenter = from.y + nodeHeight / 2;
  const toCenter = to.y + nodeHeight / 2;
  if (to.x >= from.x + nodeWidth) {
    const start = { x: from.x + nodeWidth, y: fromCenter };
    const end = { x: to.x, y: toCenter };
    const middle = (start.x + end.x) / 2;
    return { edge, path: elbow(start, end, middle), labelX: (middle + end.x) / 2, labelY: end.y - 7 };
  }
  if (to.x + nodeWidth <= from.x) {
    const start = { x: from.x, y: fromCenter };
    const end = { x: to.x + nodeWidth, y: toCenter };
    const middle = (start.x + end.x) / 2;
    return { edge, path: elbow(start, end, middle), labelX: (middle + end.x) / 2, labelY: end.y - 7 };
  }
  const start = { x: from.x + nodeWidth, y: fromCenter };
  const end = { x: to.x + nodeWidth, y: toCenter };
  const loop = Math.max(start.x, end.x) + loopOffset;
  return { edge, path: elbow(start, end, loop), labelX: loop + 6, labelY: (start.y + end.y) / 2 };
}

// M départ, H jusqu'au coude, deux quarts de cercle autour de la verticale,
// H jusqu'à l'arrivée. Sur la même ligne, un trait droit.
function elbow(start: Box, end: Box, turnX: number): string {
  const direction = Math.sign(end.x - start.x) || 1;
  const rise = end.y - start.y;
  if (Math.abs(rise) < 1) {
    return `M${start.x} ${start.y} H${end.x}`;
  }
  const radius = Math.min(cornerRadius, Math.abs(rise) / 2);
  const down = Math.sign(rise);
  const before = turnX - direction * radius;
  const after = turnX + direction * radius;
  return [
    `M${start.x} ${start.y}`,
    `H${before}`,
    `Q${turnX} ${start.y} ${turnX} ${start.y + down * radius}`,
    `V${end.y - down * radius}`,
    `Q${turnX} ${end.y} ${after} ${end.y}`,
    `H${end.x}`,
  ].join(" ");
}

// Le cadrage qui fait tenir tout le graphe dans la vue, sans agrandir
// au-delà de la taille réelle.
export interface View {
  scale: number;
  x: number;
  y: number;
}

export function fitView(layout: Layout, width: number, height: number): View {
  if (width <= 0 || height <= 0 || layout.width <= 0) {
    return { scale: 1, x: 0, y: 0 };
  }
  const scale = Math.min(1, width / layout.width, height / layout.height);
  return { scale, x: (width - layout.width * scale) / 2, y: (height - layout.height * scale) / 2 };
}

export const minScale = 0.4;
export const maxScale = 2;

// Zoome autour du centre de la vue.
export function zoomView(view: View, factor: number, width: number, height: number): View {
  const scale = Math.min(maxScale, Math.max(minScale, view.scale * factor));
  const ratio = scale / view.scale;
  return { scale, x: width / 2 - (width / 2 - view.x) * ratio, y: height / 2 - (height / 2 - view.y) * ratio };
}

// Les services qui partagent un réseau avec celui-ci, par réseau : ce
// qu'il peut joindre, sans que ce soit une dépendance.
export interface Peers {
  network: string;
  services: Service[];
}

export function peersOf(service: Service, all: Service[]): Peers[] {
  const peers: Peers[] = [];
  for (const attachment of service.networks) {
    const services = all.filter((other) => other.id !== service.id && other.networks.some((candidate) => candidate.network_id === attachment.network_id));
    if (services.length > 0) {
      peers.push({ network: attachment.name, services });
    }
  }
  return peers;
}

// Les ports publics d'une machine, uniques, pour le sous-titre d'Internet.
export function publicPorts(edges: Edge[]): number[] {
  const ports = new Set<number>();
  for (const edge of edges) {
    if (edge.kind === "public" && edge.port !== undefined) {
      ports.add(edge.port);
    }
  }
  return [...ports].sort((a, b) => a - b);
}

// La vue d'ensemble : le même dessin, moins détaillé. Un seul nœud Internet,
// puis chaque machine dans un cadre, ses services en nœuds compacts : les
// publiés dans la première colonne, face à Internet, les autres en grille.
// Ni groupes ni dépendances : l'onglet Réseau de la machine les dessine.
// Une seule machine n'a pas de cadre.

export const compactWidth = 148;
export const compactHeight = 40;
const compactGap = 12;
const frameGap = 20;
const framePadding = { top: 30, right: 16, bottom: 14, left: 16 };
const maxColumns = 4;
// La hauteur d'un cadre sans service : sa légende seule.
const emptyFrameHeight = 22;

export interface PlacedMachine {
  id: string;
  name: string;
  online: boolean;
  x: number;
  y: number;
  width: number;
  height: number;
  empty: boolean;
}

export interface OverviewLayout {
  width: number;
  height: number;
  framed: boolean;
  internet: PlacedNode;
  machines: PlacedMachine[];
  nodes: PlacedNode[];
  edges: PlacedEdge[];
}

export function layoutOverview(response: OverviewNetworkResponse): OverviewLayout {
  const framed = response.machines.length > 1;
  const internet: PlacedNode = { id: internetID, x: margin, y: margin, service: null, public: false };
  const machines: PlacedMachine[] = [];
  const nodes: PlacedNode[] = [];
  const edges: PlacedEdge[] = [];
  const left = margin + nodeWidth + columnGap;
  let y = margin;
  let widest = 0;
  for (const machine of response.machines) {
    const placed = placeMachine(machine, left, y, framed);
    machines.push(placed.frame);
    nodes.push(...placed.nodes);
    widest = Math.max(widest, placed.frame.width);
    y += placed.frame.height + frameGap;
  }
  const height = Math.max(y - frameGap, margin + nodeHeight) + margin;
  internet.y = (height - nodeHeight) / 2;
  const start = { x: internet.x + nodeWidth, y: internet.y + nodeHeight / 2 };
  for (const machine of response.machines) {
    for (const edge of machine.edges) {
      const target = nodes.find((node) => node.id === edge.to);
      if (edge.kind !== "public" || target === undefined) {
        continue;
      }
      const end = { x: target.x, y: target.y + compactHeight / 2 };
      const middle = (start.x + end.x) / 2;
      edges.push({ edge, path: elbow(start, end, middle), labelX: (middle + end.x) / 2, labelY: end.y - 7 });
    }
  }
  return { width: left + widest + margin, height, framed, internet, machines, nodes, edges };
}

// Les services publiés en première colonne, les autres en grille à droite.
function placeMachine(machine: NetworkResponse, left: number, top: number, framed: boolean): { frame: PlacedMachine; nodes: PlacedNode[] } {
  const published = new Set(machine.edges.filter((edge) => edge.kind === "public").map((edge) => edge.to));
  const publicServices = machine.services.filter((service) => published.has(service.id));
  const others = machine.services.filter((service) => !published.has(service.id));
  const gridColumns = publicServices.length > 0 ? maxColumns - 1 : maxColumns;
  const rows = Math.max(publicServices.length, Math.ceil(others.length / gridColumns), 1);
  const usedColumns = (publicServices.length > 0 ? 1 : 0) + Math.min(gridColumns, Math.ceil(others.length / rows));
  const padding = framed ? framePadding : { top: 0, right: 0, bottom: 0, left: 0 };
  const nodes: PlacedNode[] = [];
  const originX = left + padding.left;
  const originY = top + padding.top;
  publicServices.forEach((service, row) => {
    nodes.push({ id: service.id, x: originX, y: originY + row * (compactHeight + compactGap), service, public: true });
  });
  const firstGridColumn = publicServices.length > 0 ? 1 : 0;
  others.forEach((service, index) => {
    const column = firstGridColumn + (index % gridColumns);
    const row = Math.floor(index / gridColumns);
    nodes.push({ id: service.id, x: originX + column * (compactWidth + compactGap), y: originY + row * (compactHeight + compactGap), service, public: false });
  });
  const empty = machine.services.length === 0;
  const contentHeight = empty ? emptyFrameHeight : rows * compactHeight + (rows - 1) * compactGap;
  const contentWidth = Math.max(usedColumns, 1) * compactWidth + (Math.max(usedColumns, 1) - 1) * compactGap;
  return {
    frame: {
      id: machine.machine_id, name: machine.machine_name, online: machine.online,
      x: left, y: top, width: padding.left + contentWidth + padding.right, height: padding.top + contentHeight + padding.bottom, empty,
    },
    nodes,
  };
}
