import { expect, test } from "vitest";

import { isFormModified, nextModal } from "./modal";

test("une fenêtre propre se ferme dès la demande", () => {
  expect(nextModal("open", "request_close", false)).toEqual({ state: "open", closes: true });
});

test("une saisie modifiée demande avant de fermer", () => {
  expect(nextModal("open", "request_close", true)).toEqual({ state: "asking", closes: false });
});

test("abandonner la saisie ferme", () => {
  expect(nextModal("asking", "abandon", true)).toEqual({ state: "open", closes: true });
});

test("continuer la saisie referme la question sans fermer la fenêtre", () => {
  expect(nextModal("asking", "keep", true)).toEqual({ state: "open", closes: false });
});

test("Échap pendant la question revient à la saisie, il ne ferme pas", () => {
  expect(nextModal("asking", "request_close", true)).toEqual({ state: "open", closes: false });
});

test("abandon ou continuer hors question ne font rien", () => {
  expect(nextModal("open", "abandon", true)).toEqual({ state: "open", closes: false });
  expect(nextModal("open", "keep", true)).toEqual({ state: "open", closes: false });
});

test("une saisie identique à l'ouverture n'est pas modifiée, un champ changé l'est", () => {
  const initial = { name: "", units: ["a"] };
  expect(isFormModified({ name: "", units: ["a"] }, initial)).toBe(false);
  expect(isFormModified({ name: "x", units: ["a"] }, initial)).toBe(true);
  expect(isFormModified({ name: "", units: ["a", "b"] }, initial)).toBe(true);
});
