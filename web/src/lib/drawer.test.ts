import { expect, test } from "vitest";

import { nextDrawer, returnsFocus } from "./drawer";

test("le bouton menu ouvre le tiroir fermé et ferme le tiroir ouvert", () => {
  expect(nextDrawer(false, "toggle")).toBe(true);
  expect(nextDrawer(true, "toggle")).toBe(false);
});

test("le voile, Échap, une navigation et un écran large ferment le tiroir", () => {
  for (const event of ["backdrop", "escape", "navigate", "wide"] as const) {
    expect(nextDrawer(true, event)).toBe(false);
    expect(nextDrawer(false, event)).toBe(false);
  }
});

test("le focus revient au bouton après une fermeture voulue, jamais sur un tiroir déjà fermé", () => {
  expect(returnsFocus(true, "escape")).toBe(true);
  expect(returnsFocus(true, "backdrop")).toBe(true);
  expect(returnsFocus(true, "navigate")).toBe(true);
  expect(returnsFocus(false, "navigate")).toBe(false);
  expect(returnsFocus(true, "wide")).toBe(false);
});
