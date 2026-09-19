import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import { findingText, formatBinding, formatBindings, formatPorts, imageBase, matchesFilter, needsAttention, nodeSubtitle, nodeTone, serviceIcon, servicePill, servicesSubtitle } from "./services";

const t: Translate = (key, ...args) => [key, ...args].join(":");

test("servicePill dérive la pastille des faits", () => {
  expect(servicePill({ state: "running", exit_code: 0, health: "" }).key).toBe("active");
  expect(servicePill({ state: "running", exit_code: 0, health: "healthy" }).key).toBe("active");
  expect(servicePill({ state: "running", exit_code: 0, health: "starting" }).key).toBe("starting");
  expect(servicePill({ state: "running", exit_code: 0, health: "unhealthy" }).key).toBe("failing");
  expect(servicePill({ state: "exited", exit_code: 0, health: "" }).key).toBe("stopped");
  expect(servicePill({ state: "exited", exit_code: 143, health: "" }).key).toBe("stopped");
  expect(servicePill({ state: "exited", exit_code: 1, health: "" }).key).toBe("failing");
  expect(servicePill({ state: "restarting", exit_code: 1, health: "" }).key).toBe("restarting");
  expect(servicePill({ state: "paused", exit_code: 0, health: "" }).key).toBe("paused");
  expect(servicePill({ state: "dead", exit_code: 0, health: "" }).tone).toBe("danger");
  expect(servicePill({ state: "created", exit_code: 0, health: "" }).key).toBe("created");
});

test("needsAttention suit la règle du serveur", () => {
  expect(needsAttention({ state: "exited", exit_code: 1, health: "" })).toBe(true);
  expect(needsAttention({ state: "exited", exit_code: 137, health: "" })).toBe(false);
  expect(needsAttention({ state: "running", exit_code: 0, health: "unhealthy" })).toBe(true);
  expect(needsAttention({ state: "restarting", exit_code: 0, health: "" })).toBe(true);
});

test("matchesFilter sépare actifs et arrêtés", () => {
  expect(matchesFilter({ state: "running" }, "active")).toBe(true);
  expect(matchesFilter({ state: "restarting" }, "active")).toBe(true);
  expect(matchesFilter({ state: "exited" }, "active")).toBe(false);
  expect(matchesFilter({ state: "paused" }, "stopped")).toBe(true);
  expect(matchesFilter({ state: "exited" }, "all")).toBe(true);
});

test("formatPorts écrit les ports publiés comme la planche", () => {
  expect(formatPorts([])).toBe("");
  expect(
    formatPorts([
      { ip: "0.0.0.0", host_port: 80, container_port: 80, protocol: "tcp" },
      { ip: "::", host_port: 80, container_port: 80, protocol: "tcp" },
      { ip: "0.0.0.0", host_port: 443, container_port: 443, protocol: "tcp" },
    ]),
  ).toBe(":80 · :443");
});

test("servicesSubtitle compte", () => {
  expect(servicesSubtitle(t, 0, 0)).toBe("services.subtitle_none");
  expect(servicesSubtitle(t, 1, 1)).toBe("services.subtitle_one_attention");
  expect(servicesSubtitle(t, 6, 0)).toBe("services.subtitle_ok:6");
  expect(servicesSubtitle(t, 6, 2)).toBe("services.subtitle_attention:6:2");
});

test("imageBase et serviceIcon lisent le nom de l'image", () => {
  expect(imageBase("nextcloud:29.0.4")).toBe("nextcloud");
  expect(imageBase("ghcr.io/lib/postgres:16.4")).toBe("postgres");
  expect(imageBase("redis@sha256:abc")).toBe("redis");
  expect(imageBase("localhost:5000/app")).toBe("app");
  expect(serviceIcon({ image: "postgres:16" })).toBe("database");
  expect(serviceIcon({ image: "nginx:alpine" })).toBe("box");
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

test("nodeTone met l'attention d'un constat sur un service sain, jamais sur un défaillant", () => {
  const warn = [{ kind: "database_port_public" as const, level: "warn" as const, port: 6379, protocol: "tcp" }];
  expect(nodeTone({ state: "running", exit_code: 0, health: "", exposure: [] })).toBe("ok");
  expect(nodeTone({ state: "running", exit_code: 0, health: "", exposure: warn })).toBe("warn");
  expect(nodeTone({ state: "exited", exit_code: 1, health: "", exposure: warn })).toBe("danger");
  expect(nodeTone({ state: "running", exit_code: 0, health: "", exposure: [{ kind: "port_public", level: "info", port: 80, protocol: "tcp" }] })).toBe("ok");
});

test("findingText passe le port au catalogue", () => {
  expect(findingText(t, { kind: "privileged", level: "warn" })).toBe("network.exposure_privileged");
  expect(findingText(t, { kind: "port_public", level: "info", port: 80, protocol: "tcp" })).toBe("network.exposure_port_public:80/tcp");
});
