import { expect, test } from "vitest";

import { readViewMode, viewModeKey, writeViewMode } from "./viewMode";

function memoryStorage(initial: Record<string, string> = {}) {
  const values = new Map(Object.entries(initial));
  return {
    values,
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    },
  };
}

test("le choix écrit se relit sous la clé de sa liste", () => {
  const storage = memoryStorage();
  writeViewMode(() => storage, "machines", "table");
  expect(storage.values.get(viewModeKey("machines"))).toBe("table");
  expect(readViewMode(() => storage, "machines", "cards")).toBe("table");
  expect(readViewMode(() => storage, "status-pages", "cards")).toBe("cards");
});

test("une valeur inconnue rend le défaut", () => {
  const storage = memoryStorage({ [viewModeKey("machines")]: "mosaic" });
  expect(readViewMode(() => storage, "machines", "cards")).toBe("cards");
});

test("un stockage qui lève rend le défaut à la lecture et ne casse rien à l'écriture", () => {
  const blocked = () => {
    throw new Error("SecurityError");
  };
  expect(readViewMode(blocked, "machines", "table")).toBe("table");
  expect(() => writeViewMode(blocked, "machines", "cards")).not.toThrow();

  const failing = {
    getItem: () => {
      throw new Error("SecurityError");
    },
    setItem: () => {
      throw new Error("QuotaExceededError");
    },
  };
  expect(readViewMode(() => failing, "machines", "cards")).toBe("cards");
  expect(() => writeViewMode(() => failing, "machines", "table")).not.toThrow();
});
