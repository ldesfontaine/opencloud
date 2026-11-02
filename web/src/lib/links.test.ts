import { describe, expect, test } from "vitest";

import type { DeclaredService } from "../api/directory";
import { emptyLinkForm, endName, freshnessText, isLinkComplete, isPortValid, linkRequestOf, peerOptions, proofTone, unitOptions } from "./links";

const t = (key: string, ...args: (string | number)[]) => [key, ...args].join("|");
const now = Date.parse("2026-11-01T12:00:00Z");

function service(id: string, name: string, machine: string, unitNames: string[]): DeclaredService {
  return {
    id,
    name,
    machine_name: machine,
    origin: "compose",
    units: unitNames.map((unit) => ({ id: `${id}-${unit}`, name: unit })),
  } as unknown as DeclaredService;
}

describe("proofTone", () => {
  test("le déclaré se tait, le reste est une affirmation à confirmer", () => {
    expect(proofTone("declared")).toBe("ok");
    expect(proofTone("deduced")).toBe("accent");
    expect(proofTone("observed")).toBe("accent");
  });
});

describe("freshnessText", () => {
  const base = { proposed: false, dismissed: false } as const;
  test("un lien d'outil se dit vu il y a, celui de l'opérateur écrit le", () => {
    expect(freshnessText(t, { ...base, signal: "depends_on", first_seen_at: "2026-10-01T00:00:00Z", last_seen_at: "2026-11-01T11:00:00Z" }, now)).toContain("link.seen|");
    expect(freshnessText(t, { ...base, signal: "operator", first_seen_at: "2026-10-01T08:00:00Z", last_seen_at: "2026-10-01T08:00:00Z" }, now)).toBe("link.written|2026-10-01");
  });
  test("une route proposée est calculée à l'instant, un lien écarté dit quand", () => {
    const proposed = { proposed: true, dismissed: false, signal: "public_url", first_seen_at: "0001-01-01T00:00:00Z", last_seen_at: "0001-01-01T00:00:00Z" } as const;
    expect(freshnessText(t, proposed, now)).toBe("link.deduced_fresh");
    expect(freshnessText(t, { ...proposed, proposed: false, dismissed: true, signal: "operator", first_seen_at: "2026-10-01T08:00:00Z" }, now)).toBe("link.dismissed_on|2026-10-01");
  });
  test("un lien lu dans l'environnement est écrit : il se dit vu, comme celui d'un outil", () => {
    expect(freshnessText(t, { ...base, signal: "environment", first_seen_at: "2026-10-01T00:00:00Z", last_seen_at: "2026-11-01T11:00:00Z" }, now)).toContain("link.seen|");
  });
});

describe("peerOptions et unitOptions", () => {
  const services = [service("a", "zeta", "vps", ["web"]), service("b", "alpha", "vps", ["web", "worker"]), service("c", "alpha", "boîtier", ["web"])];
  test("tous les autres services, par machine puis par nom", () => {
    expect(peerOptions(services, "a").map((option) => `${option.hint}/${option.label}`)).toEqual(["boîtier/alpha", "vps/alpha"]);
  });
  test("les unités ne se proposent que s'il y a un choix", () => {
    expect(unitOptions(services[0])).toEqual([]);
    expect(unitOptions(undefined)).toEqual([]);
    expect(unitOptions(services[1]).map((option) => option.label)).toEqual(["web", "worker"]);
  });
});

describe("isPortValid", () => {
  test("vide ou entre 1 et 65535", () => {
    expect(["", " ", "1", "65535"].every(isPortValid)).toBe(true);
    expect(["0", "65536", "-3", "80.5", "http"].some(isPortValid)).toBe(false);
  });
});

describe("isLinkComplete", () => {
  const single = service("a", "a", "m", ["web"]);
  const several = service("b", "b", "m", ["web", "worker"]);
  test("il faut un service en face, et l'unité de chaque côté qui en a plusieurs", () => {
    const values = { ...emptyLinkForm(), peerServiceID: "b" };
    expect(isLinkComplete(values, single, undefined)).toBe(false);
    expect(isLinkComplete(values, single, several)).toBe(false);
    expect(isLinkComplete({ ...values, peerUnitID: "b-web" }, single, several)).toBe(true);
    expect(isLinkComplete({ ...values, peerUnitID: "b-web", selfUnitID: "" }, several, several)).toBe(false);
    expect(isLinkComplete({ ...values, peerUnitID: "b-web", port: "99999" }, single, several)).toBe(false);
  });
});

describe("linkRequestOf", () => {
  const values = { ...emptyLinkForm(), peerServiceID: "peer", peerUnitID: "pu", selfUnitID: "su", port: "5432" };
  test("« utilise » part de ce service", () => {
    expect(linkRequestOf("me", values)).toEqual({ from_service_id: "me", from_unit_id: "su", to_service_id: "peer", to_unit_id: "pu", port: 5432 });
  });
  test("« est utilisé par » y arrive, sans port c'est zéro", () => {
    expect(linkRequestOf("me", { ...values, direction: "incoming", port: "" })).toEqual({ from_service_id: "peer", from_unit_id: "pu", to_service_id: "me", to_unit_id: "su", port: 0 });
  });
});

describe("endName", () => {
  test("le service déclaré d'abord, l'instance sinon", () => {
    expect(endName({ service_name: "caddy", label: "caddy.service" })).toBe("caddy");
    expect(endName({ service_name: "", label: "legacy-cache" })).toBe("legacy-cache");
  });
});
