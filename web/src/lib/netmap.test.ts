import { expect, test } from "vitest";

import type { MapBox, MapLink, MapService, NetworkMap, ServiceState } from "../api/network";
import { type Rect, allBoxes, anchorOf, anchorPath, badgeText, boxAnchorOf, boxTarget, chipTarget, drawnEdges, drawnLinks, foldedHosts, guestZones, guestsOf, isHidden, isLit, linkTip, portsText, serviceIndex, shownServices, stateTone, worstState } from "./netmap";

function service(machine: string, label: string, state: ServiceState = "running", extra: Partial<MapService> = {}): MapService {
  return { id: `s:${machine}|${label}`, label, kind: "compose", state, replicas: 1, container_id: "", service_id: "", ...extra };
}

function box(id: string, services: MapService[] = [], extra: Partial<MapBox> = {}): MapBox {
  return {
    id: `m:${id}`, machine_id: id, name: id, kind: "vm", vmid: 0, node: "", managed: true, online: true, guest_status: "", address: "", zone_id: "", services, host_id: "", guest_count: 0, folded: false, ...extra,
  };
}

// Un hôte et ses invités, à plat : chaque invité nomme son hôte.
function host(id: string, guests: MapBox[], extra: Partial<MapBox> = {}): MapBox[] {
  return [box(id, [], { kind: "physical", guest_count: guests.length, ...extra }), ...guests.map((guest) => ({ ...guest, host_id: `m:${id}` }))];
}

function link(id: string, from: string, to: string, extra: Partial<MapLink> = {}): MapLink {
  return { id, from, to, nature: "order", proof: "declared", signal: "depends_on", port: 0, stale: false, proposed: false, first_seen_at: "", last_seen_at: "", ...extra };
}

function mapOf(home: MapBox[], links: MapLink[] = [], outside: MapBox[] = []): NetworkMap {
  return { zones: [], outside, home: [{ zone_id: "z", boxes: home }], empty_zones: [], folding: false, firewalls: [], tunnels: [], entries: [], links };
}

test("allBoxes rend le dehors puis les couloirs, guestsOf les invités d'un hôte où qu'ils soient", () => {
  const map = mapOf([...host("h", [box("g1"), box("g2")]), box("p")], [], [box("vps", [], { kind: "vps" })]);
  expect(allBoxes(map).map((found) => found.name)).toEqual(["vps", "h", "g1", "g2", "p"]);
  expect(guestsOf(map, "m:h").map((found) => found.name)).toEqual(["g1", "g2"]);
});

test("les hôtes repliés sont ceux du serveur, sauf le choix de l'opérateur qui l'emporte", () => {
  const map = mapOf([...host("calme", [box("g1")], { folded: true }), ...host("alerte", [box("g2")])]);
  expect([...foldedHosts(map, new Map())]).toEqual(["m:calme"]);
  expect([...foldedHosts(map, new Map([["m:calme", false], ["m:alerte", true]]))]).toEqual(["m:alerte"]);
  expect(foldedHosts(mapOf([...host("h", [box("g")])]), new Map()).size).toBe(0);
});

test("un invité d'un hôte replié est caché, l'hôte et les autres non", () => {
  const [hostBox, guest] = host("h", [box("g")]) as [MapBox, MapBox];
  const folded = new Set(["m:h"]);
  expect(isHidden(guest, folded)).toBe(true);
  expect(isHidden(hostBox, folded)).toBe(false);
  expect(isHidden(box("p"), folded)).toBe(false);
  expect(isHidden(guest, new Set())).toBe(false);
});

test("un hôte survolé met en évidence sa carte et celles de ses invités, pas les autres", () => {
  const [hostBox, guest] = host("h", [box("g")]) as [MapBox, MapBox];
  expect([hostBox, guest, box("p")].map((found) => isLit(found, "m:h"))).toEqual([true, true, false]);
  expect(isLit(guest, null)).toBe(false);
});

test("guestZones dit les zones des invités d'un hôte, dans l'ordre de la carte", () => {
  const map: NetworkMap = {
    ...mapOf([]),
    home: [
      { zone_id: "a", boxes: [box("h", [], { guest_count: 2 }), box("g1", [], { host_id: "m:h", zone_id: "a" })] },
      { zone_id: "b", boxes: [box("g2", [], { host_id: "m:h", zone_id: "b" }), box("x", [], { zone_id: "b" })] },
      { zone_id: "c", boxes: [box("y", [], { zone_id: "c" })] },
    ],
  };
  expect(guestZones(map, "m:h")).toEqual(["a", "b"]);
});

test("un service d'un hôte replié a pour ancre la carte de l'hôte", () => {
  const web = service("g", "web");
  const map = mapOf([...host("h", [box("g", [web])]), box("p", [service("p", "backup")])]);
  expect(anchorOf(map, web.id, new Set())).toBe(web.id);
  expect(anchorOf(map, web.id, new Set(["m:h"]))).toBe("m:h");
  expect(anchorOf(map, "s:p|backup", new Set(["m:h"]))).toBe("s:p|backup");
  expect(anchorOf(map, "s:inconnu|x", new Set())).toBeNull();
  expect(boxAnchorOf(map, "m:g", new Set(["m:h"]))).toBe("m:h");
  expect(boxAnchorOf(map, "m:g", new Set())).toBe("m:g");
  expect(boxAnchorOf(map, "m:perdu", new Set())).toBeNull();
});

test("drawnLinks saute les liens sans ancre et ceux qui restent dans un hôte replié", () => {
  const a = service("g1", "a");
  const b = service("g2", "b");
  const c = service("p", "c");
  const map = mapOf(
    [...host("h", [box("g1", [a]), box("g2", [b])]), box("p", [c])],
    [link("l1", a.id, b.id), link("l2", a.id, c.id), link("l3", a.id, "s:perdu|x")],
  );
  expect(drawnLinks(map, new Set()).map((drawn) => drawn.link.id)).toEqual(["l1", "l2"]);
  const folded = drawnLinks(map, new Set(["m:h"]));
  expect(folded.map((drawn) => [drawn.link.id, drawn.from, drawn.to])).toEqual([["l2", "m:h", c.id]]);
});

test("des liens repliés sur la même paire n'en font qu'un, le frais l'emporte sur l'ancien", () => {
  const a = service("g1", "a");
  const b = service("g2", "b");
  const c = service("p", "c");
  const map = mapOf(
    [...host("h", [box("g1", [a]), box("g2", [b])]), box("p", [c])],
    [link("vieux", a.id, c.id, { stale: true }), link("frais", b.id, c.id)],
  );
  const drawn = drawnLinks(map, new Set(["m:h"]));
  expect(drawn).toHaveLength(1);
  expect(drawn[0]?.link.id).toBe("frais");
});

test("shownServices met les services en défaut d'abord et compte le reste", () => {
  const services = ["a", "b", "c", "d"].map((label) => service("m", label));
  services[3] = service("m", "d", "failed");
  const { shown, hidden } = shownServices(box("m", services), 2);
  expect(shown.map((found) => found.label)).toEqual(["d", "a"]);
  expect(hidden).toBe(2);
});

test("une puce mène au service déclaré, sinon au conteneur, sinon à la machine", () => {
  const machine = box("m1");
  expect(chipTarget(service("m1", "x", "running", { service_id: "svc_1", container_id: "c1" }), machine)).toBe("/services/annuaire/svc_1");
  expect(chipTarget(service("m1", "x", "running", { container_id: "c1" }), machine)).toBe("/services/conteneurs/c1");
  expect(chipTarget(service("m1", "x"), machine)).toBe("/machines/m1");
  expect(boxTarget(machine)).toBe("/machines/m1");
  expect(boxTarget(box("", [], { managed: false, machine_id: "" }))).toBe("");
});

test("portsText joint les ports", () => {
  expect(portsText([443, 80])).toBe("443, 80");
});

test("anchorPath sort du côté qui fait face : à droite, à gauche, en bas, en haut", () => {
  const left: Rect = { left: 0, top: 0, right: 100, bottom: 40 };
  const right: Rect = { left: 200, top: 100, right: 300, bottom: 140 };
  expect(anchorPath(left, right).path).toBe("M100 20 C150 20 150 120 200 120");
  expect(anchorPath(right, left).path).toBe("M200 120 C150 120 150 20 100 20");
  const above: Rect = { left: 0, top: 0, right: 100, bottom: 40 };
  const below: Rect = { left: 20, top: 140, right: 120, bottom: 180 };
  expect(anchorPath(above, below).path).toBe("M50 40 C50 90 70 90 70 140");
  expect(anchorPath(below, above).path).toBe("M70 140 C70 90 50 90 50 40");
});

test("anchorPath d'un tunnel arrive par le haut quand l'ancre de départ est entièrement au-dessus", () => {
  const vps: Rect = { left: 300, top: 0, right: 500, bottom: 60 };
  const chip: Rect = { left: 20, top: 200, right: 120, bottom: 224 };
  expect(anchorPath(vps, chip, true).path).toBe("M400 60 C400 130 70 130 70 200");
  // Sans l'option, la même paire sort par le côté.
  expect(anchorPath(vps, chip).path).toBe("M300 30 C210 30 210 212 120 212");
  // Côte à côte (se recouvrent en hauteur), l'option ne change rien.
  const side: Rect = { left: 600, top: 190, right: 700, bottom: 230 };
  expect(anchorPath(chip, side, true)).toEqual(anchorPath(chip, side));
});

test("anchorPath garde une portée minimale quand les ancres se touchent", () => {
  const a: Rect = { left: 0, top: 0, right: 100, bottom: 40 };
  const b: Rect = { left: 100, top: 10, right: 200, bottom: 50 };
  expect(anchorPath(a, b).path).toBe("M100 20 C128 20 72 30 100 30");
});

test("serviceIndex nomme la machine de chaque puce, invités compris", () => {
  const web = service("g", "web");
  const map = mapOf(host("h", [box("g", [web])]));
  const spot = serviceIndex(map).get(web.id);
  expect(spot?.box.name).toBe("g");
  expect(spot?.service.label).toBe("web");
});

const translate = (key: string, ...args: unknown[]) => [key, ...args].join(":");

test("badgeText ajoute le numéro d'invité au type", () => {
  expect(badgeText(translate, { kind: "vm", vmid: 5010 })).toBe("machine.kind_vm 5010");
  expect(badgeText(translate, { kind: "physical", vmid: 0 })).toBe("machine.kind_physical");
});

test("stateTone ne rend du vert, de l'orange ou du rouge que pour un état connu", () => {
  expect(["running", "degraded", "failed", "stopped", "missing", "unknown"].map((state) => stateTone(state as ServiceState))).toEqual(["ok", "warn", "danger", "neutral", "neutral", "neutral"]);
});

test("linkTip nomme les deux bouts, la preuve, le port et la fraîcheur", () => {
  const web = service("g", "web");
  const db = service("p", "db");
  const map = mapOf([box("g", [web]), box("p", [db])]);
  const now = Date.parse("2026-09-12T12:00:00Z");
  const tip = linkTip(translate, link("l", web.id, db.id, { port: 5432, signal: "operator", first_seen_at: "2026-09-10T09:00:00Z" }), serviceIndex(map), now);
  expect(tip.title).toBe("web (g) → db (p)");
  expect(tip.lines).toEqual(["link.proof_declared · link.signal_operator", "link.nature_order", "link.port:5432", "link.written:2026-09-10"]);
  const proposed = linkTip(translate, link("l", web.id, db.id, { proposed: true, proof: "deduced", stale: true }), serviceIndex(map), now);
  expect(proposed.lines).toContain("network.map_link_proposed");
  expect(proposed.lines).toContain("link.stale");
});

test("drawnEdges pose les entrées, les liens et les tunnels, et aucun trait d'interface vers une machine", () => {
  const relay = service("vps", "relais");
  const vault = service("v", "vault");
  const proxy = service("p", "proxy");
  const map: NetworkMap = {
    zones: [{ id: "z", name: "SERVICES", source: "firewall", cidr: "", vlan: 0, tone: 3 }],
    outside: [box("vps", [relay], { kind: "vps" })],
    home: [{ zone_id: "z", boxes: [...host("h", [box("v", [vault], { zone_id: "z" })]), box("p", [proxy])] }],
    empty_zones: [],
    folding: false,
    firewalls: [{ id: "fw", name: "pare-feu", interfaces: [
      { name: "lan", device: "igc1", role: "SERVICES", cidr: "", up: true, zone_id: "z", wan: false, tunnel: false },
      { name: "wg", device: "wg0", role: "", cidr: "", up: true, zone_id: "", wan: false, tunnel: true },
    ] }],
    tunnels: [{ id: "t", label: "WireGuard", proof: "observed", outside: "m:vps", service: relay.id, firewall: "fw", interface: "wg" }],
    entries: [{ to: proxy.id, ports: [443], observed: false }],
    links: [link("l1", proxy.id, vault.id)],
  };
  const edges = drawnEdges(map, new Set());
  expect(edges.map((edge) => `${edge.kind} ${edge.from} > ${edge.to}`)).toEqual([
    `entry entry:${proxy.id} > ${proxy.id}`,
    `link ${proxy.id} > ${vault.id}`,
    "tunnel m:vps > iface:fw:wg",
  ]);
  // Le tunnel va de la carte de la machine du dehors à l'interface du tunnel.
  expect(edges.some((edge) => edge.from.startsWith("iface:"))).toBe(false);
  // Un hôte replié reçoit les traits de ses invités.
  expect(drawnEdges(map, new Set(["m:h"])).find((edge) => edge.kind === "link")?.to).toBe("m:h");
  // Sans pare-feu, le tunnel finit chez soi ; sans machine, il n'est pas tracé.
  const bare = { ...map, firewalls: [], tunnels: [{ ...(map.tunnels[0] as NetworkMap["tunnels"][number]), firewall: "" }] };
  expect(drawnEdges(bare, new Set()).filter((edge) => edge.kind === "tunnel").map((edge) => `${edge.from} > ${edge.to}`)).toEqual(["m:vps > home"]);
  const orphan = { ...map, tunnels: [{ ...(map.tunnels[0] as NetworkMap["tunnels"][number]), outside: "" }] };
  expect(drawnEdges(orphan, new Set()).filter((edge) => edge.kind === "tunnel")).toEqual([]);
});

test("worstState dit le pire état d'un hôte, invités compris, et rien quand tout va", () => {
  const calm = mapOf(host("h", [box("g", [service("g", "web")])]));
  expect(worstState(calm, calm.home[0]?.boxes[0] as MapBox)).toBeNull();
  const unwell = mapOf(host("h", [box("g", [service("g", "web", "degraded")]), box("k", [service("k", "db", "failed")])]));
  expect(worstState(unwell, unwell.home[0]?.boxes[0] as MapBox)).toBe("failed");
  const slightly = mapOf(host("h", [box("g", [service("g", "web", "degraded")])]));
  expect(worstState(slightly, slightly.home[0]?.boxes[0] as MapBox)).toBe("degraded");
});
