import { expect, test } from "vitest";

import type { Topic } from "../api/refresh";
import { finishAction, refreshAfter } from "./useAction";

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

test("après une écriture réussie : le sujet, onDone, puis onSettled", () => {
  const calls: string[] = [];
  const refresh = (topic?: Topic) => calls.push(`refresh ${topic ?? "all"}`);
  finishAction(
    refresh,
    { topic: "alerts", onDone: (result: number) => calls.push(`done ${result}`), onSettled: () => calls.push("settled") },
    { done: true, result: 7 },
  );
  expect(calls).toEqual(["refresh alerts", "done 7", "settled"]);
});

test("après une écriture refusée : seul onSettled passe", () => {
  const calls: string[] = [];
  const refresh = (topic?: Topic) => calls.push(`refresh ${topic ?? "all"}`);
  finishAction(refresh, { topic: "alerts", onDone: () => calls.push("done"), onSettled: () => calls.push("settled") }, { done: false });
  expect(calls).toEqual(["settled"]);
});
