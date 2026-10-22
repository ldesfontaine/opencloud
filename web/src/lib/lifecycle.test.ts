import { expect, test } from "vitest";

import type { Lifecycle } from "../api/lifecycle";
import type { LifecycleSettings } from "../api/settings";
import type { Translate } from "../i18n/context";
import { formatDay, lifecycleHasDetails, lifecycleText, lifecycleTones, newerLine, readingText } from "./lifecycle";

const t: Translate = (key, ...args) => [key, ...args].join(":");

function lifecycle(overrides: Partial<Lifecycle>): Lifecycle {
  return {
    status: "maintained", product: "PostgreSQL", product_url: "https://endoflife.date/postgresql", cycle: "16",
    eol_from: "2028-11-09", latest: "16.15", newer: [], fetched_at: "2026-10-12T06:00:00Z", is_stale: false, ...overrides,
  };
}

function settings(overrides: Partial<LifecycleSettings>): LifecycleSettings {
  return { enabled: true, source: "endoflife.date", fetched_at: null, products: 0, is_failing: false, last_attempt_at: null, ...overrides };
}

const now = Date.parse("2026-10-12T09:00:00Z");

test("formatDay écrit le jour dans la langue, sans glisser de fuseau", () => {
  expect(formatDay("2028-11-09", "fr")).toBe("9 novembre 2028");
  expect(formatDay("2028-11-09", "en")).toBe("November 9, 2028");
  expect(formatDay("2026-01-01", "fr")).toBe("1 janvier 2026");
  expect(formatDay("bientôt", "fr")).toBe("bientôt");
});

test("lifecycleText dit la branche et jusqu'à quand", () => {
  expect(lifecycleText(t, lifecycle({}), "fr", "image")).toBe("lifecycle.maintained_until:PostgreSQL:16:9 novembre 2028");
  expect(lifecycleText(t, lifecycle({ eol_from: "" }), "fr", "image")).toBe("lifecycle.maintained:PostgreSQL:16");
  expect(lifecycleText(t, lifecycle({ status: "eol", product: "Traefik", cycle: "3.1", eol_from: "2024-10-28" }), "fr", "image")).toBe(
    "lifecycle.eol_since:Traefik:3.1:28 octobre 2024",
  );
  expect(lifecycleText(t, lifecycle({ status: "eol", eol_from: "" }), "fr", "image")).toBe("lifecycle.eol:PostgreSQL:16");
});

test("lifecycleText dit ce qu'il ne sait pas", () => {
  expect(lifecycleText(t, lifecycle({ status: "unknown_version", cycle: "" }), "fr", "image")).toBe("lifecycle.unknown_version:PostgreSQL");
  expect(lifecycleText(t, lifecycle({ status: "unknown_product", product: "" }), "fr", "image")).toBe("lifecycle.unknown_product");
  expect(lifecycleText(t, lifecycle({ status: "pending", product: "" }), "fr", "image")).toBe("lifecycle.pending");
});

test("newerLine dit la dernière version et la date quand la source les donne", () => {
  expect(newerLine(t, { cycle: "17", latest: "17.11", eol_from: "2029-11-08" }, "fr")).toBe("lifecycle.newer_full:17:17.11:8 novembre 2029");
  expect(newerLine(t, { cycle: "3.7", latest: "3.7.14", eol_from: "" }, "fr")).toBe("lifecycle.newer_latest:3.7:3.7.14");
  expect(newerLine(t, { cycle: "18", latest: "", eol_from: "2030-11-14" }, "fr")).toBe("lifecycle.newer_until:18:14 novembre 2030");
  expect(newerLine(t, { cycle: "19", latest: "", eol_from: "" }, "fr")).toBe("19");
});

test("lifecycleText parle d'un système avec ses mots", () => {
  const debian = lifecycle({ product: "Debian", cycle: "12", eol_from: "2028-06-30" });
  expect(lifecycleText(t, debian, "fr", "system")).toBe("lifecycle.maintained_until:Debian:12:30 juin 2028");
  expect(lifecycleText(t, lifecycle({ status: "unknown_version", product: "Debian", cycle: "" }), "fr", "system")).toBe("lifecycle.unknown_version_system:Debian");
  expect(lifecycleText(t, lifecycle({ status: "unknown_product", product: "" }), "fr", "system")).toBe("lifecycle.unknown_system");
  expect(lifecycleText(t, lifecycle({ status: "pending", product: "" }), "fr", "system")).toBe("lifecycle.pending_system");
});

test("seule la fin de support demande l'opérateur", () => {
  expect(lifecycleTones.eol).toBe("warn");
  expect(lifecycleTones.maintained).toBe("ok");
  expect(lifecycleTones.unknown_product).toBe("neutral");
});

test("readingText dit la dernière lecture, ou qu'elle a échoué", () => {
  expect(readingText(t, settings({}), now)).toBe("lifecycle.reading_none");
  const done = readingText(t, settings({ fetched_at: "2026-10-12T06:00:00Z", products: 4 }), now);
  expect(done.startsWith("lifecycle.reading_done:")).toBe(true);
  expect(done.endsWith(":4")).toBe(true);
  const failedFirst = readingText(t, settings({ is_failing: true, last_attempt_at: "2026-10-12T08:50:00Z" }), now);
  expect(failedFirst.startsWith("lifecycle.reading_failed_first:")).toBe(true);
  const failed = readingText(t, settings({ is_failing: true, last_attempt_at: "2026-10-12T08:50:00Z", fetched_at: "2026-10-11T06:00:00Z" }), now);
  expect(failed.startsWith("lifecycle.reading_failed:")).toBe(true);
});

test("lifecycleHasDetails : une carte sous l'historique seulement s'il y a plus que la ligne de l'en-tête", () => {
  const bare = { latest: "", newer: [], is_stale: false, fetched_at: null };
  expect(lifecycleHasDetails(lifecycle({ ...bare, status: "pending" }))).toBe(false);
  expect(lifecycleHasDetails(lifecycle({ ...bare, status: "unknown_product" }))).toBe(false);
  expect(lifecycleHasDetails(lifecycle({ ...bare, status: "maintained", latest: "12.13" }))).toBe(true);
  expect(lifecycleHasDetails(lifecycle({ ...bare, status: "unknown_version", latest: "12.13" }))).toBe(false);
  expect(lifecycleHasDetails(lifecycle({ ...bare, status: "eol", newer: [{ cycle: "13", latest: "", eol_from: "" }] }))).toBe(true);
  expect(lifecycleHasDetails(lifecycle({ ...bare, status: "unknown_product", is_stale: true }))).toBe(true);
  expect(lifecycleHasDetails(lifecycle({ ...bare, status: "maintained", fetched_at: "2026-10-12T06:00:00Z" }))).toBe(true);
});
