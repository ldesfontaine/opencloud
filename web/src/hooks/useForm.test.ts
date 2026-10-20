import { expect, test } from "vitest";

import { shouldFill } from "./useForm";

test("la source remplit le formulaire à sa première arrivée, et jamais après", () => {
  const job = { name: "sauvegarde" };
  expect(shouldFill(false, null)).toBe(false);
  expect(shouldFill(false, job)).toBe(true);
  // Une relecture rend un objet neuf : déjà rempli, le formulaire garde la saisie.
  expect(shouldFill(true, { name: "sauvegarde" })).toBe(false);
  expect(shouldFill(true, null)).toBe(false);
});
