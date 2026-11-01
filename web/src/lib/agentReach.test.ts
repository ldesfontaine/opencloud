import { expect, test } from "vitest";

import { offlineAmong } from "./agentReach";

const machines = [
  { id: "a", online: true },
  { id: "b", online: false },
];

test("seules les machines demandées et hors ligne sont rendues", () => {
  expect(offlineAmong(machines, ["a", "b"])).toEqual(["b"]);
  expect(offlineAmong(machines, ["a"])).toEqual([]);
});

test("une machine inconnue de la liste ne bloque pas", () => {
  expect(offlineAmong(machines, ["z"])).toEqual([]);
  expect(offlineAmong([], ["b"])).toEqual([]);
});
