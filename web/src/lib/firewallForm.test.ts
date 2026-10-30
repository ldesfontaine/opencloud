import { expect, test } from "vitest";

import { isCredential, isHTTPSAddress } from "./firewallForm";

test("l'adresse de l'API est en https et nomme un hôte", () => {
  expect(isHTTPSAddress("https://192.0.2.1")).toBe(true);
  expect(isHTTPSAddress("  https://fw.lan:8443 ")).toBe(true);
  expect(isHTTPSAddress("http://fw.lan")).toBe(false);
  expect(isHTTPSAddress("https://")).toBe(false);
  expect(isHTTPSAddress("")).toBe(false);
});

test("une clé ou un secret compte de 8 à 200 caractères imprimables sans espace", () => {
  expect(isCredential("abcdefgh")).toBe(true);
  expect(isCredential("abcdefg")).toBe(false);
  expect(isCredential("abcd efgh")).toBe(false);
  expect(isCredential("é".repeat(10))).toBe(false);
  expect(isCredential("a".repeat(201))).toBe(false);
});
