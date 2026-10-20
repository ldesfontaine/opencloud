import { expect, test } from "vitest";

import { proposedExportPath } from "./manifest";

test("l'export avec propositions est l'export, avec propose=1", () => {
  expect(proposedExportPath).toBe("/api/manifest/export?propose=1");
});
