import { expect, test } from "vitest";

import { rowClickAction, type RowClickFacts } from "./rowLink";

const plain: RowClickFacts = { button: 0, withModifier: false, onControl: false, selectingText: false };

test("un clic simple sur la ligne suit le lien du nom", () => {
  expect(rowClickAction(plain)).toBe("follow");
});

test("Ctrl, Cmd ou Maj et le bouton du milieu ouvrent la fiche dans un autre onglet", () => {
  expect(rowClickAction({ ...plain, withModifier: true })).toBe("new-tab");
  expect(rowClickAction({ ...plain, button: 1 })).toBe("new-tab");
});

test("un clic sur un contrôle de la ligne, ou pendant une sélection de texte, laisse la ligne en place", () => {
  expect(rowClickAction({ ...plain, onControl: true })).toBe("ignore");
  expect(rowClickAction({ ...plain, onControl: true, button: 1 })).toBe("ignore");
  expect(rowClickAction({ ...plain, selectingText: true })).toBe("ignore");
});

test("le bouton droit ne fait rien : le menu du navigateur reste", () => {
  expect(rowClickAction({ ...plain, button: 2 })).toBe("ignore");
});
