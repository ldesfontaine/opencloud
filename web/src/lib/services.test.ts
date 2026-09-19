import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import { formatPorts, matchesFilter, needsAttention, servicePill, servicesSubtitle } from "./services";

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
