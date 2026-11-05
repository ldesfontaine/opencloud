import { expect, test } from "vitest";

import type { ThresholdRule } from "../api/thresholds";
import { isWindowed, ruleSentence, thresholdError, thresholdFormOf, thresholdRequestOf } from "./thresholds";

const disk: ThresholdRule = {
  resource: "disk", attention: 85, danger: 95, recovery: 80, window_minutes: 0, origin: "default",
  min_attention: 50, max_danger: 99, min_window_minutes: 0, max_window_minutes: 0,
};
const cpu: ThresholdRule = {
  resource: "cpu", attention: 95, danger: 99, recovery: 90, window_minutes: 15, origin: "default",
  min_attention: 50, max_danger: 100, min_window_minutes: 1, max_window_minutes: 120,
};

test("thresholdError accepte la règle du serveur, bornes comprises", () => {
  expect(thresholdError({ attention: 85, danger: 95, window_minutes: 0 }, disk)).toBeNull();
  expect(thresholdError({ attention: 50, danger: 99, window_minutes: 0 }, disk)).toBeNull();
  expect(thresholdError({ attention: 95, danger: 100, window_minutes: 1 }, cpu)).toBeNull();
  expect(thresholdError({ attention: 95, danger: 100, window_minutes: 120 }, cpu)).toBeNull();
});

test("thresholdError refuse un seuil hors bornes, inversé, égal ou non entier", () => {
  for (const request of [
    { attention: 49, danger: 95, window_minutes: 0 },
    { attention: 85, danger: 100, window_minutes: 0 },
    { attention: 90, danger: 90, window_minutes: 0 },
    { attention: 95, danger: 85, window_minutes: 0 },
    { attention: 85.5, danger: 95, window_minutes: 0 },
    { attention: Number(""), danger: 95, window_minutes: 0 },
  ]) {
    expect(thresholdError(request, disk)).toBe("settings.threshold_invalid");
  }
});

test("thresholdError exige une fenêtre dans ses bornes, et refuse celle d'un volume", () => {
  expect(thresholdError({ attention: 95, danger: 99, window_minutes: 0 }, cpu)).toBe("settings.threshold_invalid");
  expect(thresholdError({ attention: 95, danger: 99, window_minutes: 121 }, cpu)).toBe("settings.threshold_invalid");
  expect(thresholdError({ attention: 95, danger: 99, window_minutes: 1.5 }, cpu)).toBe("settings.threshold_invalid");
  expect(thresholdError({ attention: 85, danger: 95, window_minutes: 5 }, disk)).toBe("settings.threshold_invalid");
});

test("isWindowed : seul le disque se juge sur la dernière mesure", () => {
  expect(isWindowed(cpu)).toBe(true);
  expect(isWindowed(disk)).toBe(false);
});

test("thresholdFormOf montre la règle en texte, thresholdRequestOf la rend en nombres", () => {
  const values = thresholdFormOf(cpu);
  expect(values).toEqual({ attention: "95", danger: "99", window: "15" });
  expect(thresholdRequestOf(values)).toEqual({ attention: 95, danger: 99, window_minutes: 15 });
});

test("ruleSentence dit la fenêtre d'une moyenne, pas celle d'un volume", () => {
  const t = (key: string, ...args: (string | number)[]) => [key, ...args].join(" ");
  expect(ruleSentence(t, cpu)).toBe("thresholds.rule_windowed 95 99 15");
  expect(ruleSentence(t, disk)).toBe("thresholds.rule_instant 85 95");
});
