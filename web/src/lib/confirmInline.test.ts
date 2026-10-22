import { expect, test } from "vitest";

import { nextConfirm, returnsFocus, shouldConfirm, type ConfirmEvent, type ConfirmState } from "./confirmInline";

test("le bouton demande, la question se referme sur confirmer, annuler ou Échap", () => {
  expect(nextConfirm("idle", "ask")).toBe("asking");
  for (const event of ["confirm", "cancel", "escape"] as const) {
    expect(nextConfirm("asking", event)).toBe("idle");
  }
});

test("hors question, confirmer, annuler et Échap ne changent rien", () => {
  for (const event of ["confirm", "cancel", "escape"] as const) {
    expect(nextConfirm("idle", event)).toBe("idle");
  }
});

test("seul « Confirmer » pendant la question lance l'écriture", () => {
  const states: ConfirmState[] = ["idle", "asking"];
  const events: ConfirmEvent[] = ["ask", "confirm", "cancel", "escape"];
  const running = states.flatMap((state) => events.filter((event) => shouldConfirm(state, event)).map((event) => `${state}/${event}`));
  expect(running).toEqual(["asking/confirm"]);
});

test("le focus revient au bouton quand la question se referme, pas avant", () => {
  expect(returnsFocus("asking", "idle")).toBe(true);
  expect(returnsFocus("idle", "asking")).toBe(false);
  expect(returnsFocus("idle", "idle")).toBe(false);
});
