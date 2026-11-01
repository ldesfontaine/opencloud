import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";

import { McpChip } from "./McpChip";

vi.mock("../i18n/context", () => ({
  useT: () => (key: string) => key,
  useIsVerbRoute: () => (route: string) => route === "DELETE /api/machines/{id}",
}));

test("supprimer la machine est un verbe à confirmation : sa route porte la puce", () => {
  expect(renderToStaticMarkup(<McpChip route="DELETE /api/machines/{id}" />)).toContain('class="mcp-chip"');
});

test("une route sans verbe n'a pas de puce", () => {
  expect(renderToStaticMarkup(<McpChip route="POST /api/machines/{id}/actions/reenroll" />)).toBe("");
});
