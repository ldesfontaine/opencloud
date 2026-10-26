import { expect, test } from "vitest";

import {
  canJumpFromBar,
  forwardTarget,
  furthestReached,
  initialPosition,
  isSummary,
  moveTo,
  previousPosition,
  stepMark,
  summaryPosition,
  type StepValidator,
} from "./wizard";

const allValid: StepValidator = () => null;

test("le récapitulatif vient après la dernière étape", () => {
  expect(summaryPosition(3)).toBe(3);
  expect(isSummary(3, 3)).toBe(true);
  expect(isSummary(2, 3)).toBe(false);
});

test("la création part de la première étape, la modification du récapitulatif", () => {
  expect(initialPosition("create", 3)).toBe(0);
  expect(initialPosition("edit", 3)).toBe(3);
});

test("Suivant avance d'une étape quand la courante est bonne", () => {
  expect(moveTo(0, 1, allValid)).toEqual({ position: 1, error: null });
});

test("Suivant reste sur l'étape en erreur et rend sa clé", () => {
  const validate: StepValidator = (index) => (index === 1 ? "job.name_invalid" : null);
  expect(moveTo(1, 2, validate)).toEqual({ position: 1, error: "job.name_invalid" });
});

test("un saut en avant valide chaque étape franchie et s'arrête à la première en erreur", () => {
  const checked: number[] = [];
  const validate: StepValidator = (index) => {
    checked.push(index);
    return index === 2 ? "probe.target_invalid" : null;
  };
  expect(moveTo(0, 4, validate)).toEqual({ position: 2, error: "probe.target_invalid" });
  expect(checked).toEqual([0, 1, 2]);
});

test("revenir en arrière ne valide rien", () => {
  const validate: StepValidator = () => "job.name_invalid";
  expect(moveTo(3, 1, validate)).toEqual({ position: 1, error: null });
  expect(previousPosition(2)).toBe(1);
  expect(previousPosition(0)).toBe(0);
});

test("avant le récapitulatif, l'action principale mène à l'étape suivante", () => {
  expect(forwardTarget(0, 3, 0)).toBe(1);
  expect(forwardTarget(2, 3, 2)).toBe(3);
});

test("après un « Modifier », l'action principale ramène au récapitulatif", () => {
  expect(forwardTarget(0, 3, 3)).toBe(3);
});

test("la barre d'étapes ne mène directement qu'en modification", () => {
  expect(canJumpFromBar("edit")).toBe(true);
  expect(canJumpFromBar("create")).toBe(false);
});

test("une étape est faite avant la plus avancée atteinte, courante, ou à venir", () => {
  expect(stepMark(1, 1, 2)).toBe("current");
  expect(stepMark(0, 1, 2)).toBe("done");
  expect(stepMark(2, 1, 2)).toBe("todo");
  expect(stepMark(3, 0, 3)).toBe("todo");
  expect(stepMark(2, 0, 3)).toBe("done");
});

test("la position la plus avancée ne recule pas sur un Précédent", () => {
  expect(furthestReached(3, 1)).toBe(3);
  expect(furthestReached(1, 2)).toBe(2);
});
