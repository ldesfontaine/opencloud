import { expect, test } from "vitest";

import { ApiError, segment } from "../api/client";
import { resolvePath } from "./useResource";

test("un chemin tel quel ou absent passe sans erreur", () => {
  expect(resolvePath("/api/machines")).toEqual({ path: "/api/machines", failure: null });
  expect(resolvePath(null)).toEqual({ path: null, failure: null });
});

test("un chemin construit avec un identifiant sain se résout", () => {
  const id = "9f86d081884c7d65";
  expect(resolvePath(() => `/api/jobs/${segment(id)}`)).toEqual({ path: `/api/jobs/${id}`, failure: null });
});

test("un identifiant forgé devient un 404 sans requête ni exception au rendu", () => {
  const resolved = resolvePath(() => `/api/jobs/${segment("../../status/components/x")}`);
  expect(resolved.path).toBeNull();
  expect(resolved.failure).toBeInstanceOf(ApiError);
  expect(resolved.failure?.status).toBe(404);
});
