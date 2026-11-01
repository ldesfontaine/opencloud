import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter, Route, Routes } from "react-router";
import { expect, test, vi } from "vitest";

import { ComponentModal } from "./ComponentModal";

vi.mock("../../i18n/context", () => ({ useT: () => (key: string) => key }));

function renderAt(path: string): string {
  return renderToStaticMarkup(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/disponibilite/pages/:id/composants/nouveau" element={<ComponentModal />} />
      </Routes>
    </MemoryRouter>,
  );
}

test("à la création, la fenêtre demande le nom et ne propose pas de projet : il vient de l'adresse", () => {
  const markup = renderAt("/disponibilite/pages/prj_1/composants/nouveau");
  expect(markup).toContain("component.new_title");
  expect(markup).toContain('id="component-name"');
  expect(markup).not.toContain('id="component-project"');
});
