import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { expect, test, vi } from "vitest";

import type { Container } from "../api/containers";
import type { DeclaredService } from "../api/directory";
import { DeclaredServiceTable } from "./DeclaredServiceTable";

// Sans serveur, la traduction rend la clé.
vi.mock("../i18n/context", () => ({
  useT: () => (key: string, ...args: unknown[]) => [key, ...args].join(":"),
  useI18n: () => ({ t: (key: string) => key, language: "fr" }),
}));

const service = (id: string, name: string) => ({ id, name, title: "", machine_id: "m1", machine_name: "vps", nature: "application", origin: "compose", units: [], project_ids: [] }) as unknown as DeclaredService;
const container = (state: string, exit_code: number) => ({ id: `c-${state}`, state, exit_code, health: "", observed: true, declared_service_id: "svc_a" }) as unknown as Container;

function render(containers: Map<string, Container[]>) {
  return renderToStaticMarkup(
    <MemoryRouter>
      <DeclaredServiceTable services={[service("svc_a", "nextcloud"), service("svc_b", "caddy")]} projects={[]} gaps={new Map()} containers={containers} />
    </MemoryRouter>,
  );
}

test("un service qui porte des conteneurs offre de les déplier, replié au départ", () => {
  const markup = render(new Map([["svc_a", [container("running", 0), container("exited", 1)]]]));
  expect(markup).toContain('aria-expanded="false"');
  expect(markup).toContain("services.containers_expand:nextcloud");
  expect(markup).toContain("services.containers_count:2");
  expect(markup).not.toContain("svc-containers");
});

test("ce qui demande attention parmi les conteneurs se dit sans les déplier", () => {
  const markup = render(new Map([["svc_a", [container("running", 0), container("exited", 1)]]]));
  expect(markup).toContain("services.containers_attention:1");
});

test("un service sans conteneur n'a ni bouton ni attention", () => {
  const markup = render(new Map());
  expect(markup).not.toContain("svc-toggle");
  expect(markup).not.toContain("services.containers_attention");
});
