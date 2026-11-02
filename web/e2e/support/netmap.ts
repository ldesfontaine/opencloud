import type { MapBox, MapLink, MapService, NetworkMap, ServiceState } from "../../src/api/network";

// Des cartes fictives (adresses de documentation, noms génériques) qui ont la
// forme exacte de GET /api/network : le parcours les sert à la place de la
// réponse du serveur pour les trois cas que les données fictives du serveur ne
// montrent pas (tout configuré, tout à plat, quarante machines). Le serveur,
// lui, prouve la déduction (internal/netmap).

function service(machine: string, label: string, state: ServiceState = "running", extra: Partial<MapService> = {}): MapService {
  return { id: `s:${machine}|compose:${label}/${label}`, label, kind: "compose", state, replicas: 1, container_id: `${machine}:${label}`, service_id: "", ...extra };
}

function systemd(machine: string, label: string, state: ServiceState = "running", extra: Partial<MapService> = {}): MapService {
  return { id: `s:${machine}|systemd:${label}.service`, label, kind: "systemd", state, replicas: 0, container_id: "", service_id: `svc_${label}`, ...extra };
}

function box(id: string, name: string, kind: MapBox["kind"], extra: Partial<MapBox> = {}): MapBox {
  return {
    id: `m:${id}`, machine_id: id, name, kind, vmid: 0, node: "", managed: true, online: true, guest_status: "", address: "", zone_id: "", services: [], groups: [], ...extra,
  };
}

function link(id: string, from: MapService, to: MapService, extra: Partial<MapLink> = {}): MapLink {
  return {
    id, from: from.id, to: to.id, nature: "config", proof: "declared", signal: "operator", port: 0, stale: false, proposed: false,
    first_seen_at: "2026-09-10T09:00:00Z", last_seen_at: "2026-09-12T11:00:00Z", ...extra,
  };
}

const zoneAdmin = "fw:fw1:lan";
const zoneServices = "fw:fw1:opt1";
const zoneDmz = "fw:fw1:opt2";
const zoneIA = "fw:fw1:opt3";

// Tout est configuré : un VPS dehors, un tunnel WireGuard, un pare-feu à
// quatre zones, un hôte Proxmox et son invité sans agent, un Pi.
export function fullMap(): NetworkMap {
  const wireguard = systemd("vps", "wg-quick@wg0", "running", { service_id: "" });
  const relay = service("vps", "relais");
  const traefik = service("proxy", "traefik");
  const sso = service("proxy", "authelia");
  const vault = service("vault", "vaultwarden");
  const forge = service("forge", "forgejo", "degraded");
  const notes = service("notes", "notes");
  const notesDb = service("notes", "notes-db", "running");
  const backup = systemd("pi", "restic", "running");
  const agent = service("lab", "hermes", "failed");
  return {
    zones: [
      { id: zoneAdmin, name: "ADMIN", source: "firewall", cidr: "10.20.11.0/24", vlan: 11, tone: 0 },
      { id: zoneServices, name: "SERVICES", source: "firewall", cidr: "10.20.50.0/24", vlan: 50, tone: 3 },
      { id: zoneDmz, name: "DMZ", source: "firewall", cidr: "10.20.60.0/24", vlan: 60, tone: 5 },
      { id: zoneIA, name: "IA", source: "firewall", cidr: "10.20.80.0/24", vlan: 80, tone: 6 },
    ],
    outside: [box("vps", "vps-edge", "vps", { address: "198.51.100.7", services: [wireguard, relay] })],
    home: [
      box("pve", "pve", "physical", {
        address: "10.20.11.2",
        zone_id: zoneAdmin,
        groups: [
          { zone_id: zoneAdmin, guests: [box("bastion", "bastion", "vm", { vmid: 1120, address: "10.20.11.20", zone_id: zoneAdmin })] },
          { zone_id: zoneServices, guests: [
            box("vault", "vault", "vm", { vmid: 5010, address: "10.20.50.10", zone_id: zoneServices, services: [vault] }),
            box("forge", "forge", "lxc", { vmid: 5011, address: "10.20.50.11", zone_id: zoneServices, services: [forge] }),
            box("notes", "notes", "vm", { vmid: 5014, address: "10.20.50.14", zone_id: zoneServices, services: [notes, notesDb] }),
          ] },
          { zone_id: zoneDmz, guests: [box("proxy", "proxy", "vm", { vmid: 6010, address: "10.20.60.10", zone_id: zoneDmz, services: [traefik, sso] })] },
          { zone_id: zoneIA, guests: [
            box("lab", "lab", "vm", { vmid: 8010, address: "10.20.80.10", zone_id: zoneIA, services: [agent] }),
            box("test", "vm-de-test", "vm", { vmid: 8011, managed: false, guest_status: "stopped", zone_id: zoneIA }),
          ] },
        ],
      }),
      box("pi", "pi", "physical", { address: "10.20.90.2", services: [backup] }),
    ],
    firewalls: [
      {
        id: "fw1",
        name: "pare-feu",
        interfaces: [
          { name: "wan", device: "igc0", role: "WAN", cidr: "203.0.113.2/24", up: true, zone_id: "", wan: true, tunnel: false },
          { name: "lan", device: "igc1", role: "ADMIN", cidr: "10.20.11.1/24", up: true, zone_id: zoneAdmin, wan: false, tunnel: false },
          { name: "opt1", device: "igc2_vlan50", role: "SERVICES", cidr: "10.20.50.1/24", up: true, zone_id: zoneServices, wan: false, tunnel: false },
          { name: "opt2", device: "igc2_vlan60", role: "DMZ", cidr: "10.20.60.1/24", up: true, zone_id: zoneDmz, wan: false, tunnel: false },
          { name: "opt3", device: "igc2_vlan80", role: "IA", cidr: "10.20.80.1/24", up: true, zone_id: zoneIA, wan: false, tunnel: false },
          { name: "opt4", device: "wg0", role: "TUNNEL", cidr: "10.99.0.2/30", up: true, zone_id: "", wan: false, tunnel: true },
        ],
      },
    ],
    tunnels: [{ id: `t:${wireguard.id}`, label: "WireGuard", proof: "observed", outside: "m:vps", service: wireguard.id, firewall: "fw1", interface: "opt4" }],
    entries: [
      { to: relay.id, ports: [443, 80], observed: false },
      { to: traefik.id, ports: [443], observed: true },
    ],
    links: [
      link("lnk_1", traefik, vault, { nature: "route", proof: "deduced", signal: "public_url", port: 8080, proposed: true, first_seen_at: "", last_seen_at: "" }),
      link("lnk_2", traefik, forge, { nature: "route", proof: "declared", port: 3000 }),
      link("lnk_3", notes, notesDb, { nature: "order", proof: "declared", signal: "depends_on", port: 5432 }),
      link("lnk_4", relay, traefik, { nature: "traffic", proof: "observed", signal: "host_connection", port: 443, last_seen_at: "2026-09-12T11:55:00Z" }),
      link("lnk_5", backup, notesDb, { nature: "config", proof: "deduced", signal: "environment", port: 5432, stale: true, last_seen_at: "2026-09-01T11:00:00Z" }),
    ],
  };
}

// Sans Proxmox, sans pare-feu, sans VPS : des machines à plat, des zones de
// sous-réseau (le nom est le CIDR).
export function flatMap(): NetworkMap {
  const web = service("a", "web");
  const db = service("b", "postgres");
  return {
    zones: [
      { id: "net:192.0.2.0/24", name: "192.0.2.0/24", source: "subnet", cidr: "192.0.2.0/24", vlan: 0, tone: 1 },
      { id: "net:198.18.5.0/24", name: "198.18.5.0/24", source: "subnet", cidr: "198.18.5.0/24", vlan: 0, tone: 4 },
    ],
    outside: [],
    home: [
      box("a", "serveur-a", "physical", { address: "192.0.2.10", zone_id: "net:192.0.2.0/24", services: [web, service("a", "cache")] }),
      box("b", "serveur-b", "vm", { address: "192.0.2.11", zone_id: "net:192.0.2.0/24", services: [db] }),
      box("c", "nas", "physical", { address: "198.18.5.4", zone_id: "net:198.18.5.0/24" }),
      box("d", "imprimante", "unknown", { address: "198.18.5.9", zone_id: "net:198.18.5.0/24" }),
    ],
    firewalls: [],
    tunnels: [],
    entries: [{ to: web.id, ports: [8080], observed: false }],
    links: [link("lnk_a", web, db, { nature: "order", proof: "declared", signal: "depends_on", port: 5432 })],
  };
}

// Quarante machines sur trois hôtes : de quoi montrer le repli. Les hôtes
// « calme » n'ont aucun service en défaut ; l'hôte « alerte » en a un.
export function bigMap(): NetworkMap {
  const hosts = ["calme-1", "calme-2", "alerte"].map((name, hostIndex) => {
    const guests = Array.from({ length: 12 }, (_, index) => {
      const id = `${name}-g${index}`;
      const state: ServiceState = name === "alerte" && index === 0 ? "failed" : "running";
      return box(id, `${name}-vm${index}`, index % 3 === 0 ? "lxc" : "vm", {
        vmid: 100 + index, address: `10.30.${hostIndex}.${10 + index}`, zone_id: "net:z", services: index % 4 === 3 ? [] : [service(id, `app${index}`, state)],
      });
    });
    return box(name, name, "physical", { address: `10.30.${hostIndex}.2`, zone_id: "net:z", groups: [{ zone_id: "net:z", guests }] });
  });
  return {
    zones: [{ id: "net:z", name: "10.30.0.0/16", source: "subnet", cidr: "10.30.0.0/16", vlan: 0, tone: 2 }],
    outside: [],
    home: [...hosts, box("pi", "pi", "physical", { address: "10.30.9.2", zone_id: "net:z", services: [systemd("pi", "restic")] })],
    firewalls: [],
    tunnels: [],
    entries: [],
    links: [],
  };
}
