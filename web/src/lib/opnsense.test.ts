import { expect, test } from "vitest";

import { opnsenseLink, opnsenseUsersPath } from "./opnsense";

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
