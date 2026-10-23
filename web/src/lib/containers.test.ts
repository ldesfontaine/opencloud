import { expect, test } from "vitest";

import type { Container, Transition } from "../api/containers";
import type { Translate } from "../i18n/context";
import { containerNeedsAttention, displayedState, filterContainers, lastStateText, findingText, formatBinding, formatBindings, formatPublishedPorts, imageBase, matchesFilter, nodeSubtitle, nodeTone, containerIcon, containerPill, servicesSubtitle, transitionPassage } from "./containers";

const t: Translate = (key, ...args) => [key, ...args].join(":");

test("containerPill dérive la pastille des faits", () => {
  expect(containerPill({ state: "running", exit_code: 0, health: "" }).key).toBe("active");
  expect(containerPill({ state: "running", exit_code: 0, health: "healthy" }).key).toBe("active");
  expect(containerPill({ state: "running", exit_code: 0, health: "starting" }).key).toBe("starting");
  expect(containerPill({ state: "running", exit_code: 0, health: "unhealthy" }).key).toBe("failing");
  expect(containerPill({ state: "exited", exit_code: 0, health: "" }).key).toBe("stopped");
  expect(containerPill({ state: "exited", exit_code: 143, health: "" }).key).toBe("stopped");
  expect(containerPill({ state: "exited", exit_code: 1, health: "" }).key).toBe("failing");
  expect(containerPill({ state: "exited", exit_code: 137, health: "", oom_killed: true })).toEqual({ key: "out_of_memory", tone: "danger" });
  expect(containerPill({ state: "exited", exit_code: 137, health: "", oom_killed: false }).key).toBe("stopped");
  // docker kill sans arrêt demandé : 137 aussi, mais « Tué » ; sans le fait (agent ancien), « Arrêté ».
  expect(containerPill({ state: "exited", exit_code: 137, health: "", killed: true })).toEqual({ key: "killed", tone: "danger" });
  expect(containerPill({ state: "exited", exit_code: 137, health: "" }).key).toBe("stopped");
  expect(containerPill({ state: "restarting", exit_code: 1, health: "" }).key).toBe("restarting");
  expect(containerPill({ state: "paused", exit_code: 0, health: "" }).key).toBe("paused");
  expect(containerPill({ state: "dead", exit_code: 0, health: "" }).tone).toBe("danger");
  expect(containerPill({ state: "created", exit_code: 0, health: "" }).key).toBe("created");
});

// Une machine qui ne signale plus : l'état connu est celui d'avant son silence.
const lost = { observed: false, machine_last_seen_at: "2026-09-19T07:00:00Z" } as const;

test("displayedState dit « inconnu », neutre, dès que le serveur dit la machine non observée", () => {
  const running = { state: "running", exit_code: 0, health: "" } as const;
  const crashed = { state: "exited", exit_code: 1, health: "" } as const;
  expect(displayedState({ ...running, observed: false })).toEqual({ key: "unknown", tone: "neutral" });
  expect(displayedState({ ...crashed, observed: false })).toEqual({ key: "unknown", tone: "neutral" });
  expect(displayedState({ ...running, observed: true })).toEqual({ key: "active", tone: "ok" });
  expect(displayedState({ ...crashed, observed: true })).toEqual({ key: "failing", tone: "danger" });
  // Sans le fait (une transition, un fait passé) : l'état se lit tel quel.
  expect(displayedState(running)).toEqual({ key: "active", tone: "ok" });
  // containerPill ne lit que les faits : c'est le « dernier état ».
  expect(containerPill({ ...running, observed: false })).toEqual({ key: "active", tone: "ok" });
});

test("lastStateText dit le dernier état et depuis quand la machine s'est tue", () => {
  const now = Date.parse("2026-09-19T12:00:00Z");
  const running = { state: "running", exit_code: 0, health: "" } as const;
  expect(lastStateText(t, { ...running, ...lost }, now)).toBe("container.last_state:container.state_active:time.hours:5");
  expect(lastStateText(t, { ...running, observed: true, machine_last_seen_at: "2026-09-19T11:59:00Z" }, now)).toBeNull();
  expect(lastStateText(t, { ...running, observed: false, machine_last_seen_at: null }, now)).toBeNull();
  expect(lastStateText(t, { state: "exited", exit_code: 1, health: "", ...lost }, now)).toBe("container.last_state:container.state_failing:time.hours:5");
});

test("containerNeedsAttention ne compte pas un conteneur dont la machine ne répond plus", () => {
  expect(containerNeedsAttention({ state: "exited", exit_code: 1, health: "", ...lost })).toBe(false);
  expect(containerNeedsAttention({ state: "exited", exit_code: 1, health: "", observed: true })).toBe(true);
});

test("containerNeedsAttention suit la règle du serveur", () => {
  expect(containerNeedsAttention({ state: "exited", exit_code: 1, health: "" })).toBe(true);
  expect(containerNeedsAttention({ state: "exited", exit_code: 137, health: "" })).toBe(false);
  expect(containerNeedsAttention({ state: "exited", exit_code: 137, health: "", oom_killed: true })).toBe(true);
  expect(containerNeedsAttention({ state: "exited", exit_code: 137, health: "", killed: true })).toBe(true);
  expect(containerNeedsAttention({ state: "running", exit_code: 0, health: "unhealthy" })).toBe(true);
  expect(containerNeedsAttention({ state: "restarting", exit_code: 0, health: "" })).toBe(true);
});

test("matchesFilter sépare actifs et arrêtés", () => {
  expect(matchesFilter({ state: "running" }, "active")).toBe(true);
  expect(matchesFilter({ state: "restarting" }, "active")).toBe(true);
  expect(matchesFilter({ state: "exited" }, "active")).toBe(false);
  expect(matchesFilter({ state: "paused" }, "stopped")).toBe(true);
  expect(matchesFilter({ state: "exited" }, "all")).toBe(true);
});

test("matchesFilter : un conteneur d'état inconnu n'est ni actif ni arrêté, seul « Tous » le garde", () => {
  expect(matchesFilter({ state: "running", observed: false }, "all")).toBe(true);
  expect(matchesFilter({ state: "running", observed: false }, "active")).toBe(false);
  expect(matchesFilter({ state: "exited", observed: false }, "stopped")).toBe(false);
  expect(matchesFilter({ state: "running", observed: true }, "active")).toBe(true);
});

test("filterContainers garde tout, les actifs ou les arrêtés selon le filtre, sans toucher à la liste reçue", () => {
  const running = { id: "a", state: "running" } as Container;
  const restarting = { id: "b", state: "restarting" } as Container;
  const exited = { id: "c", state: "exited" } as Container;
  const containers = [running, restarting, exited];
  expect(filterContainers(containers, "all")).toEqual([running, restarting, exited]);
  expect(filterContainers(containers, "active")).toEqual([running, restarting]);
  expect(filterContainers(containers, "stopped")).toEqual([exited]);
  expect(filterContainers([], "active")).toEqual([]);
  expect(containers).toHaveLength(3);
});

test("formatPublishedPorts écrit les ports publiés comme la planche", () => {
  expect(formatPublishedPorts([])).toBe("");
  expect(
    formatPublishedPorts([
      { ip: "0.0.0.0", host_port: 80, container_port: 80, protocol: "tcp" },
      { ip: "::", host_port: 80, container_port: 80, protocol: "tcp" },
      { ip: "0.0.0.0", host_port: 443, container_port: 443, protocol: "tcp" },
    ]),
  ).toBe(":80 · :443");
});

test("servicesSubtitle compte", () => {
  expect(servicesSubtitle(t, 0, 0, 0)).toBe("services.subtitle_none");
  expect(servicesSubtitle(t, 1, 0, 0)).toBe("services.subtitle_one");
  expect(servicesSubtitle(t, 1, 1, 0)).toBe("services.subtitle_one_attention");
  expect(servicesSubtitle(t, 6, 0, 0)).toBe("services.subtitle_ok:6");
  expect(servicesSubtitle(t, 6, 2, 0)).toBe("services.subtitle_attention:6:2");
});

test("servicesSubtitle ne dit pas « tous actifs » de conteneurs dont l'état est inconnu", () => {
  expect(servicesSubtitle(t, 1, 0, 1)).toBe("services.subtitle_one_unknown");
  expect(servicesSubtitle(t, 6, 0, 2)).toBe("services.subtitle_unknown:6:2");
  expect(servicesSubtitle(t, 6, 1, 2)).toBe("services.subtitle_attention_unknown:6:1:2");
});

test("imageBase et containerIcon lisent le nom de l'image", () => {
  expect(imageBase("nextcloud:29.0.4")).toBe("nextcloud");
  expect(imageBase("ghcr.io/lib/postgres:16.4")).toBe("postgres");
  expect(imageBase("redis@sha256:abc")).toBe("redis");
  expect(imageBase("localhost:5000/app")).toBe("app");
  expect(containerIcon({ image: "postgres:16" })).toBe("database");
  expect(containerIcon({ image: "nginx:alpine" })).toBe("box");
});

test("nodeSubtitle et formatBinding écrivent les ports avec leur interface", () => {
  const ports = [
    { ip: "0.0.0.0", host_port: 18081, container_port: 80, protocol: "tcp" },
    { ip: "::", host_port: 18081, container_port: 80, protocol: "tcp" },
    { ip: "127.0.0.1", host_port: 15432, container_port: 5432, protocol: "tcp" },
  ];
  expect(nodeSubtitle({ image: "alpine:3.20", ports })).toBe("alpine · :18081 · 127.0.0.1:15432");
  expect(formatBinding(ports[0]!)).toBe("0.0.0.0:18081 → 80/tcp");
  expect(formatBinding(ports[2]!)).toBe("127.0.0.1:15432 → 5432/tcp");
  expect(formatBindings(ports)).toEqual(["0.0.0.0:18081 → 80/tcp", "127.0.0.1:15432 → 5432/tcp"]);
});

test("nodeTone met l'attention d'un constat sur un container sain, jamais sur un défaillant", () => {
  const warn = [{ kind: "database_port_public" as const, level: "warn" as const, port: 6379, protocol: "tcp" }];
  expect(nodeTone({ state: "running", exit_code: 0, health: "", exposure: [] })).toBe("ok");
  expect(nodeTone({ state: "running", exit_code: 0, health: "", exposure: warn })).toBe("warn");
  expect(nodeTone({ state: "exited", exit_code: 1, health: "", exposure: warn })).toBe("danger");
  expect(nodeTone({ state: "running", exit_code: 0, health: "", exposure: [{ kind: "port_public", level: "info", port: 80, protocol: "tcp" }] })).toBe("ok");
});

test("nodeTone : un nœud inconnu reste neutre, même avec un constat d'exposition", () => {
  const warn = [{ kind: "database_port_public" as const, level: "warn" as const, port: 6379, protocol: "tcp" }];
  expect(nodeTone({ state: "running", exit_code: 0, health: "", exposure: warn, observed: false })).toBe("neutral");
  expect(nodeTone({ state: "exited", exit_code: 1, health: "", exposure: [], observed: false })).toBe("neutral");
});

test("findingText passe le port au catalogue", () => {
  expect(findingText(t, { kind: "privileged", level: "warn" })).toBe("network.exposure_privileged");
  expect(findingText(t, { kind: "port_public", level: "info", port: 80, protocol: "tcp" })).toBe("network.exposure_port_public:80/tcp");
});

const transition: Transition = {
  id: 1,
  at: "2026-10-22T08:00:00Z",
  action: "start",
  previous_state: "exited",
  new_state: "running",
  previous_health: "",
  new_health: "",
  exit_code: null,
  oom_killed: false,
  killed: false,
  replayed: false,
  snippet: "",
};

test("transitionPassage dit le passage d'un état à l'autre", () => {
  expect(transitionPassage(t, transition)).toBe("container.state_stopped → container.state_active");
});

test("transitionPassage dit le passage d'une santé à l'autre", () => {
  const health = { ...transition, action: "health_status", previous_state: "running", previous_health: "healthy", new_health: "unhealthy" } as const;
  expect(transitionPassage(t, health)).toBe("container.health_healthy → container.health_unhealthy");
});

test("transitionPassage garde le titre seul sans état d'avant", () => {
  const seen = { ...transition, action: "inventory", previous_state: "" } as const;
  expect(transitionPassage(t, seen)).toBe("container.transition_seen");
  const gone = { ...transition, action: "gone", new_state: "" } as const;
  expect(transitionPassage(t, gone)).toBe("container.transition_gone");
});

test("transitionPassage garde le titre d'une destruction", () => {
  const destroyed = { ...transition, action: "destroy", previous_state: "exited", new_state: "" } as const;
  expect(transitionPassage(t, destroyed)).toBe("container.transition_destroyed");
});
