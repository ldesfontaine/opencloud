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

const service = (id: string, name: string, extra: Record<string, unknown> = {}) =>
  ({ id, name, title: "", machine_id: "m1", machine_name: "vps", nature: "application", category: "", category_deduced: "application", origin: "compose", compose_project: "", units: [{ image: name }], project_ids: [], ...extra }) as unknown as DeclaredService;
const container = (state: string, exit_code: number) => ({ id: `c-${state}`, state, exit_code, health: "", observed: true, declared_service_id: "svc_a" }) as unknown as Container;

function render(containers: Map<string, Container[]>, services = [service("svc_a", "nextcloud"), service("svc_b", "caddy")]) {
  return renderToStaticMarkup(
    <MemoryRouter>
      <DeclaredServiceTable services={services} projects={[]} gaps={new Map()} containers={containers} />
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

test("les services d'un même projet Compose sont rangés sous lui, un par ligne", () => {
  const edge = { compose_project: "edge", machine_name: "vm-edge" };
  const markup = render(new Map(), [service("1", "traefik", { ...edge, category_deduced: "web" }), service("2", "authelia", { ...edge, category_deduced: "sso" }), service("3", "whoami")]);
  expect(markup).toContain("services.group_project:edge");
  expect(markup).toContain("services.group_none");
  expect(markup).toContain("directory.category_web");
  expect(markup).toContain("directory.category_sso");
  // Le projet vient avant « sans projet ».
  expect(markup.indexOf("services.group_project:edge")).toBeLessThan(markup.indexOf("services.group_none"));
});

test("un type corrigé par l'opérateur l'emporte sur celui de l'image", () => {
  const markup = render(new Map(), [service("1", "proxy", { category: "security", category_deduced: "web" })]);
  expect(markup).toContain("directory.category_security");
  expect(markup).not.toContain("directory.category_web");
});

test("le même service sur deux machines est une ligne qui se déplie, repliée au départ", () => {
  const markup = render(new Map(), [service("1", "traefik", { machine_name: "vm-a", compose_project: "edge" }), service("2", "traefik", { machine_id: "m2", machine_name: "vm-b", compose_project: "edge" })]);
  expect(markup).toContain("services.instances_count:2");
  expect(markup).toContain("services.instances_expand:traefik");
  expect(markup).not.toContain("svc-instance");
});
