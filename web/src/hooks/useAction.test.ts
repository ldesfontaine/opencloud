import { expect, test } from "vitest";

import type { Topic } from "../api/refresh";
import { refreshAfter } from "./useAction";

test("la relecture vise le sujet demandé, tout, ou rien", () => {
  const calls: (Topic | undefined)[][] = [];
  const refresh = (...args: (Topic | undefined)[]) => {
    calls.push(args);
  };
  refreshAfter(refresh, "probes");
  refreshAfter(refresh, "all");
  refreshAfter(refresh, undefined);
  expect(calls).toEqual([["probes"], []]);
});
