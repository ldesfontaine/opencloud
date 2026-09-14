import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import { bytesIn, formatBytes, formatRate, percentOf } from "./units";

const french: Record<string, string> = {
  "unit.b": "o",
  "unit.kb": "Ko",
  "unit.mb": "Mo",
  "unit.gb": "Go",
  "unit.tb": "To",
  "unit.per_second": "%s/s",
};

const t: Translate = (key, ...args) => (french[key] ?? key).replace("%s", String(args[0] ?? ""));

test("formatBytes choisit l'unité et garde une décimale sous dix", () => {
  expect(formatBytes(t, "fr", 0)).toBe("0 o");
  expect(formatBytes(t, "fr", 512)).toBe("512 o");
  expect(formatBytes(t, "fr", 3_328_599_654)).toBe("3,1 Go");
  expect(formatBytes(t, "fr", 44_023_414_784)).toBe("41 Go");
  expect(formatBytes(t, "en", 8 * 1024 ** 4)).toBe("8 To");
});

test("bytesIn lit une quantité dans l'unité d'une autre", () => {
  expect(bytesIn("fr", 3_328_599_654, 8 * 1024 ** 3)).toBe("3,1");
  expect(bytesIn("fr", 0, 2 * 1024 ** 3)).toBe("0");
});

test("formatRate ajoute la seconde", () => {
  expect(formatRate(t, "fr", 1_258_291)).toBe("1,2 Mo/s");
  expect(formatRate(t, "en", 0)).toBe("0 o/s");
});

test("percentOf arrondit et ne divise jamais par zéro", () => {
  expect(percentOf(41, 80)).toBe(51);
  expect(percentOf(3, 0)).toBe(0);
  expect(percentOf(90, 80)).toBe(100);
});
