import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";
import { bigMap, flatMap, fullMap } from "./support/netmap";
import type { NetworkMap } from "../src/api/network";

// Le chantier R1 : la carte Réseau de la vue d'ensemble, en arbre. Les données
// fictives du serveur donnent la carte réelle ; les trois cas qu'elles ne
// montrent pas (tout configuré, tout à plat, quarante machines) sont servis
// dans la forme exacte de GET /api/network. La déduction, elle, est prouvée
// côté serveur (internal/netmap, api_network_map_test.go).

async function serve(page: Page, map: NetworkMap): Promise<void> {
  await page.route("**/api/network", (route) => route.fulfill({ json: map }));
}

test("la carte réelle : le dehors, Internet, la maison, les invités dans leur hôte", async ({ page, findings }) => {
  findings.where = "/ (carte Réseau)";
  await page.goto("/");
  const map = page.locator(".netmap");
  await expect(map).toBeVisible();
  await expect(map.locator(".netmap-outside")).toContainText("vps-paris-1");
  await expect(map.locator(".netmap-internet")).toContainText(label("network.internet"));
  // Une VM n'est jamais à côté de son hôte : l'invité est dans la boîte de l'hôte.
  await expect(map.locator(".netmap-box.is-host .netmap-box", { hasText: "opencloud" })).toBeVisible();
  await expect(map.locator(".netmap-firewall")).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("un hôte se replie et se déplie, Tout replier les ferme tous", async ({ page, findings }) => {
  findings.where = "/ (repli de la carte)";
  await serve(page, fullMap());
  await page.goto("/");
  const host = page.locator(".netmap-box", { hasText: "pve" }).first();
  await expect(host.locator(".netmap-zone")).toHaveCount(4);
  await page.getByRole("button", { name: label("network.map_fold").replace("%s", "pve") }).click();
  await expect(host.locator(".netmap-zone")).toHaveCount(0);
  await page.getByRole("button", { name: label("network.map_unfold").replace("%s", "pve") }).click();
  await expect(host.locator(".netmap-zone")).toHaveCount(4);
  await page.getByRole("button", { name: label("network.map_fold_all"), exact: true }).click();
  await expect(host.locator(".netmap-zone")).toHaveCount(0);
  await page.getByRole("button", { name: label("network.map_unfold_all"), exact: true }).click();
  await expect(host.locator(".netmap-zone")).toHaveCount(4);
  expect(findings.unknown()).toEqual([]);
});

test("un lien dit sa preuve au survol, et la liste des liens la redit", async ({ page, findings }) => {
  findings.where = "/ (liens de la carte)";
  await serve(page, fullMap());
  await page.goto("/");
  await expect(page.locator(".netmap-edge.link")).toHaveCount(5);
  // Le clavier dit la même chose que le survol (et un trait qui en croise un autre reste atteignable).
  await page.locator(".netmap-edge.link.proof-declared").first().focus();
  await expect(page.locator(".netmap-tip")).toContainText(label("link.proof_declared"));
  await page.locator(".netmap-wrap summary", { hasText: label("network.map_links_title").replace("%s", "5") }).click();
  const rows = page.locator(".netmap-wrap tbody tr");
  await expect(rows).toHaveCount(5);
  await expect(rows.filter({ hasText: label("network.map_link_proposed") })).toHaveCount(1);
  expect(findings.unknown()).toEqual([]);
});

test("le tunnel chiffré, le pare-feu entre les zones et les entrées d'Internet", async ({ page, findings }) => {
  findings.where = "/ (tunnel de la carte)";
  await serve(page, fullMap());
  await page.goto("/");
  await expect(page.locator(".netmap-edge.tunnel")).toHaveCount(1);
  await expect(page.locator(".netmap-edge.entry")).toHaveCount(2);
  await expect(page.locator(".netmap-edge.zone")).not.toHaveCount(0);
  await page.locator(".netmap-edge.tunnel .netmap-hit").hover({ force: true });
  await expect(page.locator(".netmap-tip")).toContainText(label("link.proof_observed"));
  expect(findings.unknown()).toEqual([]);
});

test("sans Proxmox, sans pare-feu, sans VPS : des machines à plat, des zones de sous-réseau", async ({ page, findings }) => {
  findings.where = "/ (carte à plat)";
  await serve(page, flatMap());
  await page.goto("/");
  await expect(page.locator(".netmap-outside")).toHaveCount(0);
  await expect(page.locator(".netmap-firewall")).toHaveCount(0);
  await expect(page.locator(".netmap-box.is-host")).toHaveCount(0);
  await expect(page.locator(".netmap-zone-tag", { hasText: "192.0.2.0/24" }).first()).toBeVisible();
  await expect(page.locator(".netmap-idle")).toContainText("nas");
  expect(findings.unknown()).toEqual([]);
});

test("à quarante machines, les hôtes sans défaut s'ouvrent repliés, celui qui a un défaut reste ouvert", async ({ page, findings }) => {
  findings.where = "/ (quarante machines)";
  await serve(page, bigMap());
  await page.goto("/");
  await expect(page.locator(".netmap-box.is-host.is-folded")).toHaveCount(2);
  await expect(page.locator(".netmap-box.is-host:not(.is-folded)")).toHaveCount(1);
  await page.getByRole("button", { name: label("network.map_unfold").replace("%s", "calme-1") }).click();
  await expect(page.locator(".netmap-box.is-host.is-folded")).toHaveCount(1);
  // Un service en défaut mène à sa fiche (les identifiants de cette carte sont fictifs : on lit l'adresse sans la suivre).
  await expect(page.locator(".netmap-chip.state-failed").first()).toHaveAttribute("href", /\/services\/conteneurs\//);
  expect(findings.unknown()).toEqual([]);
});
