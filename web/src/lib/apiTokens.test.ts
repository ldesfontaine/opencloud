import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import type { APIToken } from "../api/apiTokens";
import {
  emptyReachForm,
  emptyTokenForm,
  envBlock,
  expiryLabel,
  hasProxmoxVerb,
  lifetimeLabel,
  parseGuestNumbers,
  reachFormOf,
  reachLines,
  reachRequestOf,
  scopeLabel,
  toggleVerb,
  tokenRequestOf,
} from "./apiTokens";

const french: Record<string, string> = {
  "api_token.lifetime_days": "%d jours",
  "api_token.lifetime_never": "Sans échéance",
  "api_token.expires_never": "jamais",
  "api_token.scope_read": "Lecture",
  "api_token.scope_write": "Écriture",
  "api_token.verbs_none_list": "aucun verbe",
  "api_token.reach_machines_value": "Machines : %s",
  "api_token.reach_guests_value": "Invités : %s",
  "api_token.reach_every_machine": "toutes les machines",
  "api_token.reach_no_machine": "aucune machine",
  "api_token.reach_no_guest": "aucun invité",
  "api_token.reach_every_guest": "tous les invités",
};

const t: Translate = (key, ...args) => {
  let text = french[key] ?? key;
  for (const arg of args) {
    text = text.replace(/%[sd]/, String(arg));
  }
  return text;
};

test("lifetimeLabel dit une durée en jours, ou sans échéance pour 0", () => {
  expect(lifetimeLabel(t, 90)).toBe("90 jours");
  expect(lifetimeLabel(t, 0)).toBe("Sans échéance");
});

test("expiryLabel dit la date, ou jamais pour un jeton sans échéance", () => {
  expect(expiryLabel(t, null, () => "x")).toBe("jamais");
  expect(expiryLabel(t, "2026-12-22T10:00:00Z", (iso) => `le ${iso}`)).toBe("le 2026-12-22T10:00:00Z");
});

test("un nouveau jeton part à l'échéance proposée, en lecture, sans verbe coché", () => {
  expect(emptyTokenForm(90)).toEqual({ name: "", days: 90, scope: "read", verbs: [], machinesMode: "all", machines: [], guestsMode: "all", guests: "" });
});

test("tokenRequestOf nomme l'échéance et la portée comme l'API", () => {
  expect(tokenRequestOf({ ...emptyTokenForm(0), name: "ci", days: 0 })).toEqual({ name: "ci", expires_in_days: 0, scope: "read" });
});

test("tokenRequestOf n'envoie les verbes qu'avec l'écriture", () => {
  expect(tokenRequestOf({ ...emptyTokenForm(30), name: "ci", scope: "write", verbs: ["probe.pause"] })).toEqual({
    name: "ci",
    expires_in_days: 30,
    scope: "write",
    verbs: ["probe.pause"],
  });
  // Cochés puis repassés en lecture : le serveur refuserait la demande.
  expect(tokenRequestOf({ ...emptyTokenForm(30), name: "ci", scope: "read", verbs: ["probe.pause"], machinesMode: "some", machines: ["m1"] })).toEqual({ name: "ci", expires_in_days: 30, scope: "read" });
});

test("toggleVerb coche puis décoche", () => {
  expect(toggleVerb([], "probe.pause")).toEqual(["probe.pause"]);
  expect(toggleVerb(["probe.pause", "job.pause"], "probe.pause")).toEqual(["job.pause"]);
});

const token = (patch: Partial<APIToken>): APIToken => ({
  id: "t",
  name: "n",
  masked: "ock_…",
  scope: "read",
  verbs: [],
  machines: null,
  guests: null,
  created_at: "2026-10-28T10:00:00Z",
  expires_at: null,
  last_used_at: null,
  ...patch,
});

test("scopeLabel dit la portée, et les verbes d'un jeton en écriture", () => {
  const labelOf = (id: string) => `<${id}>`;
  expect(scopeLabel(t, token({}), labelOf)).toBe("Lecture");
  expect(scopeLabel(t, token({ scope: "write", verbs: ["a.b", "c.d"] }), labelOf)).toBe("Écriture : <a.b>, <c.d>");
  expect(scopeLabel(t, token({ scope: "write" }), labelOf)).toBe("Écriture : aucun verbe");
});

test("envBlock écrit les trois lignes du .env de mcp-maison", () => {
  const token = ["ock", "exemple"].join("_");
  expect(envBlock({ mcp_host: "127.0.0.1", opencloud_url: "http://127.0.0.1:8080" }, token)).toBe(
    `MCP_HOST=127.0.0.1\nOPENCLOUD_URL=http://127.0.0.1:8080\nOPENCLOUD_TOKEN=${token}`,
  );
});

test("sans limite, la demande ne dit rien des machines ni des invités", () => {
  const request = tokenRequestOf({ ...emptyTokenForm(30), name: "ci", scope: "write", verbs: ["proxmox.vm.start"] });
  expect(request).toEqual({ name: "ci", expires_in_days: 30, scope: "write", verbs: ["proxmox.vm.start"] });
  expect(request).not.toHaveProperty("machines");
  expect(request).not.toHaveProperty("guests");
});

test("limiter aux machines cochées envoie la liste, même vide", () => {
  const some = { ...emptyTokenForm(30), name: "ci", scope: "write" as const, verbs: ["agent.ping"], machinesMode: "some" as const };
  expect(tokenRequestOf({ ...some, machines: ["m1", "m2"] })).toMatchObject({ machines: ["m1", "m2"] });
  expect(tokenRequestOf(some)).toMatchObject({ machines: [] });
});

test("les invités ne partent qu'avec un verbe Proxmox coché, triés et sans doublon", () => {
  const form = { ...emptyTokenForm(30), name: "ci", scope: "write" as const, guestsMode: "some" as const, guests: "5012, 200 200" };
  expect(tokenRequestOf({ ...form, verbs: ["agent.ping"] })).not.toHaveProperty("guests");
  expect(tokenRequestOf({ ...form, verbs: ["proxmox.vm.start"] })).toMatchObject({ guests: [200, 5012] });
});

test("un numéro d'invité invalide bloque la demande", () => {
  const form = { ...emptyTokenForm(30), name: "ci", scope: "write" as const, verbs: ["proxmox.vm.start"], guestsMode: "some" as const };
  expect(tokenRequestOf({ ...form, guests: "200 abc" })).toBeNull();
  expect(tokenRequestOf({ ...form, guests: "0" })).toBeNull();
  expect(tokenRequestOf({ ...form, guests: "1234567890" })).toBeNull();
  expect(tokenRequestOf({ ...form, guests: "" })).toMatchObject({ guests: [] });
});

test("parseGuestNumbers lit des entiers séparés par des espaces ou des virgules", () => {
  expect(parseGuestNumbers("")).toEqual([]);
  expect(parseGuestNumbers(" 7 ,3\n5 ")).toEqual([3, 5, 7]);
  expect(parseGuestNumbers("1.5")).toBeNull();
  expect(parseGuestNumbers("-3")).toBeNull();
});

test("hasProxmoxVerb ne reconnaît que les verbes proxmox.", () => {
  expect(hasProxmoxVerb(["agent.ping", "proxmox.vm.snapshot"])).toBe(true);
  expect(hasProxmoxVerb(["agent.ping", "probe.pause"])).toBe(false);
});

test("reachFormOf relit la portée d'un jeton, et null reste « toutes »", () => {
  expect(reachFormOf(token({ scope: "write" }))).toEqual(emptyReachForm());
  expect(reachFormOf(token({ scope: "write", machines: ["m1"], guests: [200, 5012] }))).toEqual({
    machinesMode: "some",
    machines: ["m1"],
    guestsMode: "some",
    guests: "200 5012",
  });
  expect(reachRequestOf(reachFormOf(token({ scope: "write", machines: [] })), false)).toEqual({ machines: [] });
});

test("reachLines dit les machines par nom, les invités s'il y a limite, et rien pour une lecture", () => {
  const machines = [{ id: "m1", name: "vps-1" }];
  expect(reachLines(t, token({}), machines)).toEqual([]);
  expect(reachLines(t, token({ scope: "write" }), machines)).toEqual(["Machines : toutes les machines"]);
  expect(reachLines(t, token({ scope: "write", machines: ["m1", "gone"], guests: [200] }), machines)).toEqual(["Machines : vps-1, gone", "Invités : 200"]);
  expect(reachLines(t, token({ scope: "write", verbs: ["proxmox.vm.start"] }), machines)).toEqual(["Machines : toutes les machines", "Invités : tous les invités"]);
  expect(reachLines(t, token({ scope: "write", machines: [], guests: [] }), machines)).toEqual(["Machines : aucune machine", "Invités : aucun invité"]);
});
