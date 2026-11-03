import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";

import { ActionButton } from "./ActionButton";

// Sans serveur, la traduction rend la clé ; le registre des verbes est celui d'un verbe.
vi.mock("../i18n/context", () => ({
  useT: () => (key: string) => key,
  useIsVerbRoute: () => (route: string) => route === "POST /api/probes/{id}/actions/pause",
}));

test("un geste qui est un verbe d'API dit en infobulle qu'un agent peut le faire, sans puce", () => {
  const markup = renderToStaticMarkup(
    <ActionButton route="POST /api/probes/{id}/actions/pause" onClick={() => undefined}>
      Pause
    </ActionButton>,
  );
  expect(markup).toContain("btn-primary");
  expect(markup).toContain('title="action.agent_hint"');
  expect(markup).not.toContain("mcp");
});

test("un geste réservé à l'interface n'a pas d'infobulle", () => {
  const markup = renderToStaticMarkup(
    <ActionButton route="POST /api/probes" onClick={() => undefined}>
      Ajouter
    </ActionButton>,
  );
  expect(markup).not.toContain("title=");
});
