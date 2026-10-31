import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";

import { ActionButton } from "./ActionButton";

// Sans serveur, la traduction rend la clé ; le registre des verbes est celui d'un verbe.
vi.mock("../i18n/context", () => ({
  useT: () => (key: string) => key,
  useIsVerbRoute: () => (route: string) => route === "POST /api/probes/{id}/actions/pause",
}));

test("un geste qui est un verbe d'API porte la puce MCP", () => {
  const markup = renderToStaticMarkup(
    <ActionButton route="POST /api/probes/{id}/actions/pause" onClick={() => undefined}>
      Pause
    </ActionButton>,
  );
  expect(markup).toContain("btn-primary");
  expect(markup).toContain('class="mcp-chip"');
  expect(markup).toContain(">action.mcp_chip<");
});

test("un geste réservé à l'interface n'a pas la puce", () => {
  const markup = renderToStaticMarkup(
    <ActionButton route="POST /api/probes" onClick={() => undefined}>
      Ajouter
    </ActionButton>,
  );
  expect(markup).not.toContain("mcp-chip");
});
