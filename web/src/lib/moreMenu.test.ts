import { expect, test } from "vitest";

import { nextItemIndex, placeMenu, returnsFocus } from "./moreMenu";

test("Échap et un choix rendent le focus au bouton, le reste non", () => {
  expect(returnsFocus("escape")).toBe(true);
  expect(returnsFocus("select")).toBe(true);
  for (const event of ["outside", "tab", "scroll"] as const) {
    expect(returnsFocus(event)).toBe(false);
  }
});

test("les flèches bouclent, Début et Fin vont aux bouts, une autre touche ne bouge rien", () => {
  expect(nextItemIndex(0, 3, "ArrowDown")).toBe(1);
  expect(nextItemIndex(2, 3, "ArrowDown")).toBe(0);
  expect(nextItemIndex(0, 3, "ArrowUp")).toBe(2);
  expect(nextItemIndex(1, 3, "Home")).toBe(0);
  expect(nextItemIndex(0, 3, "End")).toBe(2);
  expect(nextItemIndex(1, 3, "a")).toBeNull();
  expect(nextItemIndex(0, 0, "ArrowDown")).toBeNull();
});

const viewport = { width: 1000, height: 800 };
const menu = { width: 180, height: 120 };

test("le menu s'ouvre sous le bouton, aligné à gauche", () => {
  const anchor = { top: 100, bottom: 130, left: 200, right: 230 };
  expect(placeMenu(anchor, menu, viewport)).toEqual({ top: 134, left: 200 });
});

test("près du bord droit, le menu s'aligne sur le bord droit du bouton", () => {
  const anchor = { top: 100, bottom: 130, left: 900, right: 930 };
  expect(placeMenu(anchor, menu, viewport)).toEqual({ top: 134, left: 750 });
});

test("près du bas, le menu passe au-dessus s'il y a la place, sinon reste dessous", () => {
  const low = { top: 700, bottom: 730, left: 200, right: 230 };
  expect(placeMenu(low, menu, viewport).top).toBe(576);
  const cramped = { top: 60, bottom: 90, left: 200, right: 230 };
  expect(placeMenu(cramped, menu, { width: 1000, height: 150 }).top).toBe(94);
});

test("sur un écran étroit, le menu ne sort jamais par la gauche", () => {
  const anchor = { top: 100, bottom: 130, left: 20, right: 50 };
  expect(placeMenu(anchor, menu, { width: 190, height: 800 }).left).toBe(8);
});
