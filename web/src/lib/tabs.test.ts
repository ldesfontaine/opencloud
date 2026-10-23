import { expect, test } from "vitest";

import { edgesOf } from "./tabs";

test("edgesOf ne dit rien quand tous les onglets tiennent", () => {
  expect(edgesOf(0, 360, 360)).toEqual({ left: false, right: false });
  expect(edgesOf(0, 360.5, 360)).toEqual({ left: false, right: false });
});

test("edgesOf signale la droite avant tout défilement", () => {
  expect(edgesOf(0, 520, 360)).toEqual({ left: false, right: true });
});

test("edgesOf signale les deux bords au milieu", () => {
  expect(edgesOf(80, 520, 360)).toEqual({ left: true, right: true });
});

test("edgesOf ne signale que la gauche une fois au bout", () => {
  expect(edgesOf(160, 520, 360)).toEqual({ left: true, right: false });
  // un scrollLeft fractionnaire ne laisse pas un fondu fantôme
  expect(edgesOf(159.4, 520, 360)).toEqual({ left: true, right: false });
});

test("edgesOf ignore un défilement d'un pixel de tolérance", () => {
  expect(edgesOf(1, 520, 360)).toEqual({ left: false, right: true });
});
