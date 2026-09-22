import { expect, test } from "vitest";

import type { NetworkResponse, Service } from "../api/types";
import { compactHeight, compactWidth, fitView, internetID, layoutNetwork, layoutOverview, nodeHeight, nodeWidth, peersOf, publicPorts, zoomView } from "./network";

function service(id: string, name: string, extra: Partial<Service> = {}): Service {
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
    update_policy: "",
    image_check: null,
    state: "running",
    exit_code: 0,
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
  services: [
    service("web", "web", { networks: [front], depends_on: [{ name: "db", source: "compose" }] }),
    service("db", "db", { networks: [back] }),
    service("cache", "cache", { networks: [back] }),
    service("lonely", "lonely", { state: "exited", network_mode: "bridge" }),
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

test("layoutNetwork garde dans la première colonne un groupe qui abrite un service publié", () => {
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
  const peers = peersOf(response.services[1]!, response.services);
  expect(peers).toHaveLength(1);
  expect(peers[0]!.network).toBe("ocfix_back");
  expect(peers[0]!.services.map((peer) => peer.name)).toEqual(["cache"]);
  expect(peersOf(response.services[3]!, response.services)).toEqual([]);
  expect(publicPorts([...response.edges, { from: internetID, to: "web", kind: "public", port: 80 }])).toEqual([80, 18081]);
});

test("layoutOverview cadre chaque machine, les publiés face à Internet", () => {
  const second: NetworkResponse = {
    ...response,
    machine_id: "n",
    machine_name: "nas",
    online: false,
    services: [service("s1", "s1", { machine_id: "n" }), service("s2", "s2", { machine_id: "n" }), service("s3", "s3", { machine_id: "n" }), service("s4", "s4", { machine_id: "n" }), service("s5", "s5", { machine_id: "n" })],
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
  expect(web.x).toBe(vps.x + 16);
  expect(db.x).toBe(web.x + compactWidth + 12);
  // Cinq services sans route publique : quatre colonnes, deux lignes.
  expect(byID.get("s5")!.y).toBe(byID.get("s1")!.y + compactHeight + 12);
  expect(byID.get("s5")!.x).toBe(byID.get("s1")!.x);
  expect(layout.edges).toHaveLength(1);
  expect(layout.edges[0]!.edge.to).toBe("web");
  expect(layout.internet.x).toBeLessThan(vps.x);
  expect(layout.width).toBeGreaterThan(nas.x + nas.width);
});

test("layoutOverview ne cadre pas une machine seule et accepte une machine vide", () => {
  const alone = layoutOverview({ machines: [response] });
  expect(alone.framed).toBe(false);
  expect(alone.machines[0]!.x).toBe(alone.nodes[0]!.x);
  const empty = layoutOverview({ machines: [{ ...response, services: [], edges: [] }] });
  expect(empty.machines[0]!.empty).toBe(true);
  expect(empty.nodes).toHaveLength(0);
  expect(empty.height).toBeGreaterThanOrEqual(nodeHeight + 48);
});
