import { expect, test } from "vitest";

import { opnsenseDhcpPrivileges, opnsenseLink, opnsenseReadPrivileges, opnsenseUsersPath } from "./opnsense";

test("le lien part de l'origine de l'adresse saisie", () => {
  expect(opnsenseLink("https://192.0.2.1", opnsenseUsersPath)).toBe("https://192.0.2.1/ui/auth/user");
  expect(opnsenseLink("  https://fw.lan:8443/ ", opnsenseUsersPath)).toBe("https://fw.lan:8443/ui/auth/user");
});

test("un chemin ou des identifiants dans l'adresse ne passent pas dans le lien", () => {
  expect(opnsenseLink("https://admin:secret@fw.lan/index.php?x=1", opnsenseUsersPath)).toBe("https://fw.lan/ui/auth/user");
});

test("pas de lien tant que l'adresse n'est pas un hôte https", () => {
  expect(opnsenseLink("", opnsenseUsersPath)).toBeNull();
  expect(opnsenseLink("http://fw.lan", opnsenseUsersPath)).toBeNull();
  expect(opnsenseLink("https://", opnsenseUsersPath)).toBeNull();
  expect(opnsenseLink("https://exa mple", opnsenseUsersPath)).toBeNull();
});

test("pour le DHCP, deux privilèges possibles et aucun d'ISC, qui n'est plus dans le cœur d'OPNsense", () => {
  expect(opnsenseDhcpPrivileges).toEqual(["Services: Dnsmasq DNS/DHCP: Settings", "Services: DHCP: Kea(v4)"]);
  expect([...opnsenseReadPrivileges, ...opnsenseDhcpPrivileges].some((name) => name.includes("ISC"))).toBe(false);
});
