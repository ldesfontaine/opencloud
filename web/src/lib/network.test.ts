import { expect, test } from "vitest";

import type { Container } from "../api/containers";
import type { NetworkResponse } from "../api/network";
import type { Probe } from "../api/probes";
import type { Translate } from "../i18n/context";
import { type PlacedMachine, compactHeight, compactWidth, fitOverview, fitPorts, fitView, internetID, layoutNetwork, layoutOverview, modeText, nodeHeight, nodeWidth, overviewCompactGap, overviewFramePadding, overviewGraphFloor, overviewGraphMaxHeight, overviewMargin, overviewScale, peersOf, probesOf, publicPorts, zoomView } from "./network";

function container(id: string, name: string, extra: Partial<Container> = {}): Container {
  return {
    id,
    machine_id: "m",
    machine_name: "vps",
    kind: "container",
    name,
    group: "ocfix",
    container_id: id,
    image: "alpine:3.20",
    image_id: "",
    compose_service: "",
    compose_dir: "",
    compose_file: "",
    update_policy: "watch",
    declared_service_id: "",
    image_check: null,
    state: "running",
    exit_code: 0,
    oom_killed: false,
    killed: false,
    health: "",
    restart_count: 0,
    ports: [],
    network_mode: "ocfix_back",
    privileged: false,
    networks: [],
    depends_on: [],
    exposure: [],
    created_at: "2026-09-19T09:00:00Z",
    started_at: null,
    finished_at: null,
    first_seen_at: "2026-09-19T09:00:00Z",
    last_seen_at: "2026-09-19T09:00:00Z",
    archived_at: null,
    current: null,
    ...extra,
  };
}

const back = { network_id: "back", name: "ocfix_back", ip: "172.20.0.2", aliases: [] };
const front = { network_id: "front", name: "ocfix_front", ip: "172.22.0.2", aliases: [] };

// web publié et hors groupe ; db et cache dans le réseau interne ; lonely
// arrêté sur le bridge.
const response: NetworkResponse = {
  machine_id: "m",
  machine_name: "vps",
  online: true,
  engine: null,
  containers: [
    container("web", "web", { networks: [front], depends_on: [{ name: "db", source: "compose" }] }),
    container("db", "db", { networks: [back] }),
    container("cache", "cache", { networks: [back] }),
    container("lonely", "lonely", { state: "exited", network_mode: "bridge" }),
  ],
  groups: [{ network_id: "back", name: "ocfix_back", internal: true, members: ["db", "cache"] }],
  edges: [
    { from: internetID, to: "web", kind: "public", port: 18081, protocol: "tcp" },
    { from: "web", to: "db", kind: "depends", source: "compose" },
    { from: "cache", to: "db", kind: "depends", source: "link" },
  ],
};

test("layoutNetwork met Internet, puis ce qu'il atteint, puis le reste", () => {
  const layout = layoutNetwork(response, "all");
  const byID = new Map(layout.nodes.map((node) => [node.id, node]));
  const internet = byID.get(internetID)!;
  const web = byID.get("web")!;
  const lonely = byID.get("lonely")!;
  const db = byID.get("db")!;
  expect(internet.x).toBeLessThan(web.x);
  // Internet est en face de la colonne qu'il atteint.
  expect(internet.y + nodeHeight / 2).toBeCloseTo(web.y + nodeHeight / 2);
  expect(web.public).toBe(true);
  // Le groupe sans membre publié et le conteneur seul vont dans la
  // dernière colonne, le groupe d'abord.
  expect(db.x).toBe(lonely.x);
  expect(db.x).toBeGreaterThan(web.x + nodeWidth);
  expect(db.y).toBeLessThan(lonely.y);
  expect(layout.groups).toHaveLength(1);
  const group = layout.groups[0]!;
  expect(db.x).toBeGreaterThan(group.x);
  expect(db.y).toBeGreaterThan(group.y);
  expect(db.y + nodeHeight).toBeLessThan(group.y + group.height);
  expect(layout.width).toBeGreaterThan(group.x + group.width);
  expect(layout.height).toBeGreaterThanOrEqual(group.y + group.height);
});

test("layoutNetwork garde dans la première colonne un groupe qui abrite un container publié", () => {
  const grouped: NetworkResponse = {
    ...response,
    groups: [{ network_id: "back", name: "ocfix_back", internal: true, members: ["web", "db", "cache"] }],
  };
  const layout = layoutNetwork(grouped, "all");
  const byID = new Map(layout.nodes.map((node) => [node.id, node]));
  const group = layout.groups[0]!;
  expect(byID.get("web")!.x).toBe(group.x + 14);
  expect(byID.get("web")!.y).toBeLessThan(byID.get("db")!.y);
  expect(byID.get("lonely")!.x).toBeGreaterThan(group.x + group.width);
});

test("layoutNetwork trace une arête par lien visible, la boucle dans la colonne", () => {
  const layout = layoutNetwork(response, "all");
  expect(layout.edges).toHaveLength(3);
  const publicEdge = layout.edges.find((placed) => placed.edge.kind === "public")!;
  expect(publicEdge.path.startsWith("M")).toBe(true);
  expect(publicEdge.path).toContain("H");
  const loop = layout.edges.find((placed) => placed.edge.from === "cache")!;
  expect(loop.path).toContain("V");
  expect(loop.labelX).toBeGreaterThan(layout.nodes.find((node) => node.id === "cache")!.x + nodeWidth);
});

test("layoutNetwork cache ce que le filtre retire, groupes vides compris", () => {
  const layout = layoutNetwork(response, "stopped");
  expect(layout.nodes.map((node) => node.id)).toEqual([internetID, "lonely"]);
  expect(layout.groups).toHaveLength(0);
  expect(layout.edges).toHaveLength(0);
});

test("fitView fait tenir le graphe sans l'agrandir, zoomView reste borné", () => {
  const layout = layoutNetwork(response, "all");
  const fitted = fitView(layout, layout.width * 2, layout.height * 2);
  expect(fitted.scale).toBe(1);
  expect(fitted.x).toBeCloseTo(layout.width / 2);
  const cramped = fitView(layout, layout.width / 2, layout.height);
  expect(cramped.scale).toBeCloseTo(0.5);
  expect(zoomView(fitted, 100, 800, 600).scale).toBe(2);
  expect(zoomView(fitted, 0.01, 800, 600).scale).toBe(0.4);
  expect(fitView(layout, 0, 0)).toEqual({ scale: 1, x: 0, y: 0 });
});

test("peersOf et publicPorts lisent les faits du serveur", () => {
  const peers = peersOf(response.containers[1]!, response.containers);
  expect(peers).toHaveLength(1);
  expect(peers[0]!.network).toBe("ocfix_back");
  expect(peers[0]!.containers.map((peer) => peer.name)).toEqual(["cache"]);
  expect(peersOf(response.containers[3]!, response.containers)).toEqual([]);
  expect(publicPorts([...response.edges, { from: internetID, to: "web", kind: "public", port: 80 }])).toEqual([80, 18081]);
});

test("layoutOverview cadre chaque machine, les publiés face à Internet", () => {
  const second: NetworkResponse = {
    ...response,
    machine_id: "n",
    machine_name: "nas",
    online: false,
    containers: [container("s1", "s1", { machine_id: "n" }), container("s2", "s2", { machine_id: "n" }), container("s3", "s3", { machine_id: "n" }), container("s4", "s4", { machine_id: "n" }), container("s5", "s5", { machine_id: "n" })],
    groups: [],
    edges: [],
  };
  const layout = layoutOverview({ machines: [response, second] });
  expect(layout.framed).toBe(true);
  expect(layout.machines).toHaveLength(2);
  const [vps, nas] = layout.machines as [typeof layout.machines[0], typeof layout.machines[0]];
  expect(nas.y).toBeGreaterThan(vps.y + vps.height);
  const byID = new Map(layout.nodes.map((node) => [node.id, node]));
  const web = byID.get("web")!;
  const db = byID.get("db")!;
  expect(web.public).toBe(true);
  expect(web.x).toBe(vps.x + overviewFramePadding.left);
  expect(db.x).toBe(web.x + compactWidth + overviewCompactGap);
  // Cinq conteneurs sans route publique : deux lignes (quatre colonnes au
  // plus), réparties trois et deux ; le cinquième sous le deuxième.
  expect(byID.get("s4")!.y).toBe(byID.get("s1")!.y + compactHeight + overviewCompactGap);
  expect(byID.get("s4")!.x).toBe(byID.get("s1")!.x);
  expect(byID.get("s5")!.x).toBe(byID.get("s2")!.x);
  expect(layout.edges).toHaveLength(1);
  expect(layout.edges[0]!.edge.to).toBe("web");
  expect(layout.internet.x).toBeLessThan(vps.x);
  expect(layout.width).toBeGreaterThan(nas.x + nas.width);
});

test("layoutOverview ne cadre pas une machine seule et accepte une machine vide", () => {
  const alone = layoutOverview({ machines: [response] });
  expect(alone.framed).toBe(false);
  expect(alone.machines[0]!.x).toBe(alone.nodes[0]!.x);
  const empty = layoutOverview({ machines: [{ ...response, containers: [], edges: [] }] });
  expect(empty.machines[0]!.empty).toBe(true);
  expect(empty.nodes).toHaveLength(0);
  // Le nœud Internet et ses marges, au moins.
  expect(empty.height).toBeGreaterThanOrEqual(nodeHeight + 2 * overviewMargin);
});

test("probesOf rend les sondes du service déclaré, et rien pour un conteneur non déclaré", () => {
  const probes = [{ id: "p1", service_id: "svc_1" }, { id: "p2", service_id: "svc_2" }, { id: "p3", service_id: "" }] as Probe[];
  expect(probesOf(probes, { declared_service_id: "svc_1" }).map((probe) => probe.id)).toEqual(["p1"]);
  // Une sonde sans conteneur ne se rattache pas à un conteneur sans service déclaré.
  expect(probesOf(probes, { declared_service_id: "" })).toEqual([]);
});

test("fitPorts écrit tous les ports quand la place suffit", () => {
  expect(fitPorts([80, 443], 40, 10)).toBe(":80 · :443");
  expect(fitPorts([], 40, 10)).toBe("");
});

test("fitPorts garde autant de ports que la place permet, puis compte le reste", () => {
  // Avec 5 signes de libellé : les trois ports font 18 signes, deux ports
  // et « +1 » en font 15, un port et « +2 » en font 8.
  expect(fitPorts([80, 443, 8080], 23, 5)).toBe(":80 · :443 · :8080");
  expect(fitPorts([80, 443, 8080], 22, 5)).toBe(":80 · :443 · +1");
  expect(fitPorts([80, 443, 8080], 20, 5)).toBe(":80 · :443 · +1");
  expect(fitPorts([80, 443, 8080], 19, 5)).toBe(":80 · +2");
});

test("fitPorts garde au moins un port, même quand la place manque", () => {
  expect(fitPorts([80, 443, 8080], 1, 10)).toBe(":80 · +2");
});

const modeTranslate: Translate = (key, ...args) => [key, ...args].join(":");

test("modeText traduit host et none, et dit un tiret quand le mode est vide", () => {
  expect(modeText(modeTranslate, { network_mode: "host" })).toBe("network.mode_host");
  expect(modeText(modeTranslate, { network_mode: "none" })).toBe("network.mode_none");
  expect(modeText(modeTranslate, { network_mode: "" })).toBe("–");
});

test("modeText rend le nom d'un réseau tel quel et raccourcit l'identifiant d'un conteneur partagé", () => {
  expect(modeText(modeTranslate, { network_mode: "ocfix_back" })).toBe("ocfix_back");
  expect(modeText(modeTranslate, { network_mode: "container:0123456789abcdef0123" })).toBe("network.mode_container:0123456789ab");
});

test("overviewGraphFloor garde le plus petit texte du dessin à 10 px au moins", () => {
  expect(11 * overviewGraphFloor).toBeGreaterThanOrEqual(10 - 1e-9);
  expect(overviewGraphMaxHeight).toBe(240);
});

test("overviewScale laisse un petit dessin à l'échelle 1", () => {
  expect(overviewScale({ width: 600, height: 200 }, 1000, 300, 0.9)).toBe(1);
});

test("overviewScale : la largeur décide quand elle réduit le plus", () => {
  expect(overviewScale({ width: 1000, height: 250 }, 500, 300, 0.9)).toBe(0.5);
});

test("overviewScale : la hauteur décide quand le dessin dépasse la hauteur maximale", () => {
  // 300 / 330 = 0,909… : au-dessus du plancher de 0,8.
  expect(overviewScale({ width: 600, height: 330 }, 1000, 300, 0.8)).toBeCloseTo(300 / 330, 10);
});

test("overviewScale : un dessin très haut s'arrête au plancher", () => {
  expect(overviewScale({ width: 600, height: 900 }, 1000, 300, 0.9)).toBe(0.9);
});

test("overviewScale : la largeur l'emporte sur le plancher, pour ne jamais défiler à l'horizontale", () => {
  expect(overviewScale({ width: 2000, height: 900 }, 1000, 300, 0.9)).toBe(0.5);
});

// Une machine de la vue d'ensemble : count conteneurs, les publics premiers
// publiés vers Internet.
function overviewMachine(id: string, count: number, publics: number): NetworkResponse {
  const containers = Array.from({ length: count }, (_, index) => container(`${id}-${index}`, `${id}-${index}`, { machine_id: id }));
  return {
    machine_id: id,
    machine_name: id,
    online: true,
    engine: null,
    containers,
    groups: [],
    edges: containers.slice(0, publics).map((published) => ({ from: internetID, to: published.id, kind: "public" as const, port: 443 })),
  };
}

function inside(layout: ReturnType<typeof layoutOverview>) {
  const frames = new Map(layout.machines.map((machine) => [machine.id, machine]));
  for (const node of layout.nodes) {
    const frame = frames.get(node.container!.machine_id)!;
    expect(node.x).toBeGreaterThanOrEqual(frame.x);
    expect(node.y).toBeGreaterThanOrEqual(frame.y);
    expect(node.x + compactWidth).toBeLessThanOrEqual(frame.x + frame.width);
    expect(node.y + compactHeight).toBeLessThanOrEqual(frame.y + frame.height);
  }
}

function overlaps(a: { x: number; y: number; width: number; height: number }, b: { x: number; y: number; width: number; height: number }) {
  return a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
}

test("layoutOverview : chaque conteneur reste dans le cadre de sa machine", () => {
  // Deux publiés et deux autres : les autres en une colonne, pas trois.
  inside(layoutOverview({ machines: [overviewMachine("a", 4, 2), overviewMachine("b", 4, 1), overviewMachine("c", 5, 0), overviewMachine("d", 14, 1)] }));
  inside(layoutOverview({ machines: [overviewMachine("a", 4, 2), overviewMachine("b", 4, 1), overviewMachine("c", 5, 0)] }, 2));
  const narrow = layoutOverview({ machines: [overviewMachine("a", 4, 2), overviewMachine("b", 1, 0)] });
  expect(narrow.machines[0]!.width).toBe(overviewFramePadding.left + 2 * compactWidth + overviewCompactGap + overviewFramePadding.right);
});

test("layoutOverview range les cadres en grille, alignés, sans chevauchement", () => {
  const layout = layoutOverview({ machines: [overviewMachine("a", 4, 2), overviewMachine("b", 3, 1), overviewMachine("c", 4, 2)] }, 2);
  expect(layout.perRow).toBe(2);
  const [a, b, c] = layout.machines as [PlacedMachine, PlacedMachine, PlacedMachine];
  expect(b.y).toBe(a.y);
  expect(b.x).toBeGreaterThan(a.x + a.width);
  expect(c.x).toBe(a.x);
  expect(c.y).toBeGreaterThan(a.y + a.height);
  // Une rangée a une seule hauteur, une colonne une seule largeur.
  expect(b.height).toBe(a.height);
  expect(c.width).toBe(a.width);
  for (const [index, frame] of layout.machines.entries()) {
    for (const other of layout.machines.slice(index + 1)) {
      expect(overlaps(frame, other)).toBe(false);
    }
  }
  expect(layout.width).toBeGreaterThanOrEqual(b.x + b.width);
  expect(layout.height).toBeGreaterThanOrEqual(c.y + c.height);
  inside(layout);
});

test("layoutOverview : la route d'un cadre de la deuxième colonne passe par les écarts", () => {
  const layout = layoutOverview({ machines: [overviewMachine("a", 2, 1), overviewMachine("b", 2, 1)] }, 2);
  const [a, b] = layout.machines as [PlacedMachine, PlacedMachine];
  const route = layout.edges.find((placed) => placed.edge.to === "b-0")!;
  // Les points d'appui de la route : aucun n'est à l'intérieur du cadre de a.
  const numbers = route.path.match(/-?\d+(\.\d+)?/g)!.map(Number);
  for (let index = 0; index + 1 < numbers.length; index += 2) {
    const x = numbers[index]!;
    const y = numbers[index + 1]!;
    expect(x > a.x && x < a.x + a.width && y > a.y && y < a.y + a.height).toBe(false);
  }
  // Elle finit sur le bord gauche du nœud publié de b.
  const target = layout.nodes.find((node) => node.id === "b-0")!;
  expect(route.path.endsWith(`L${target.x} ${target.y + compactHeight / 2}`)).toBe(true);
  expect(target.x).toBe(b.x + 14);
});

test("fitOverview empile les cadres quand la largeur manque, comme avant", () => {
  const response = { machines: [overviewMachine("a", 4, 1), overviewMachine("b", 4, 1), overviewMachine("c", 4, 1)] };
  // 1 070 px : la largeur d'un portable 1366 moins la barre latérale.
  const fitted = fitOverview(response, 1070, 240, 10 / 11);
  expect(fitted.layout.perRow).toBe(1);
  expect(fitted.scale).toBeGreaterThanOrEqual(10 / 11);
  expect(fitted.layout.height * fitted.scale).toBeLessThanOrEqual(240 + 0.5);
});

test("fitOverview met les cadres côte à côte quand ils tiennent, pour gagner en hauteur", () => {
  const response = { machines: [overviewMachine("a", 4, 2), overviewMachine("b", 4, 2), overviewMachine("c", 4, 2)] };
  const stacked = layoutOverview(response, 1);
  const fitted = fitOverview(response, 1070, 240, 10 / 11);
  expect(fitted.layout.perRow).toBe(2);
  expect(fitted.scale).toBeGreaterThanOrEqual(10 / 11);
  expect(fitted.layout.width * fitted.scale).toBeLessThanOrEqual(1070);
  expect(fitted.layout.height * fitted.scale).toBeLessThan(stacked.height * overviewScale(stacked, 1070, 240, 10 / 11));
});

test("fitOverview ne passe jamais sous le plancher pour ranger côte à côte", () => {
  const response = { machines: [overviewMachine("a", 4, 1), overviewMachine("b", 4, 1)] };
  // Trop étroit pour deux cadres de quatre colonnes au plancher.
  const fitted = fitOverview(response, 1070, 100, 10 / 11);
  expect(fitted.layout.perRow).toBe(1);
  // Assez large : deux cadres côte à côte, le texte à sa taille.
  const wide = fitOverview(response, 1700, 100, 10 / 11);
  expect(wide.layout.perRow).toBe(2);
  expect(wide.scale).toBeGreaterThanOrEqual(10 / 11);
});

test("fitOverview garde le plus grand texte quand plusieurs rangements tiennent", () => {
  const response = { machines: [overviewMachine("a", 4, 1), overviewMachine("b", 4, 1), overviewMachine("c", 4, 1), overviewMachine("d", 4, 1)] };
  // 1 624 px (un écran 1920) : empilés, quatre cadres demandent de réduire ; côte à côte, l'échelle 1 tient.
  const fitted = fitOverview(response, 1624, 240, 10 / 11);
  expect(fitted.scale).toBe(1);
  expect(fitted.layout.perRow).toBeGreaterThan(1);
  // Une machine seule n'a pas de cadre, rien à ranger.
  expect(fitOverview({ machines: [overviewMachine("a", 4, 1)] }, 1624, 240, 10 / 11).layout.perRow).toBe(1);
});
