import { expect, test } from "vitest";

import { displayPin, formatFingerprint, sameFingerprint } from "./fingerprint";

test("l'empreinte se lit par paires, en majuscules", () => {
  expect(formatFingerprint("ab12cd")).toBe("AB:12:CD");
  expect(formatFingerprint("")).toBe("");
});

test("une saisie avec deux-points et majuscules vaut l'empreinte relevée", () => {
  expect(sameFingerprint(" AB:12:CD ", "ab12cd")).toBe(true);
  expect(sameFingerprint("ab12ce", "ab12cd")).toBe(false);
});

test("sans empreinte relevée, rien n'est identique", () => {
  expect(sameFingerprint("", "")).toBe(false);
});

test("le récapitulatif montre un SHA-256 par paires, et une saisie douteuse telle quelle", () => {
  const hex = "ab".repeat(32);
  expect(displayPin(hex)).toBe(Array(32).fill("AB").join(":"));
  expect(displayPin(`${Array(32).fill("AB").join(":")}`)).toBe(Array(32).fill("AB").join(":"));
  expect(displayPin(" pas une empreinte ")).toBe("pas une empreinte");
});
