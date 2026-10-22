import { expect, test } from "vitest";

import { hasChanged } from "./changes";

test("identique : rien à enregistrer", () => {
  expect(hasChanged({ attention: "85", danger: "95" }, { attention: "85", danger: "95" })).toBe(false);
});

test("un champ qui diffère : à enregistrer", () => {
  expect(hasChanged({ attention: "90", danger: "95" }, { attention: "85", danger: "95" })).toBe(true);
});

test("une valeur remise à l'identique redevient « rien à enregistrer »", () => {
  const saved = { host: "smtp.example.fr", clearPassword: false };
  expect(hasChanged({ ...saved, clearPassword: true }, saved)).toBe(true);
  expect(hasChanged({ ...saved, clearPassword: false }, saved)).toBe(false);
});
