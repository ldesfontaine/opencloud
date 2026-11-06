import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";
import { bigMap, demoMap, flatMap } from "./support/netmap";
import type { NetworkMap } from "../src/api/network";

// La carte Réseau de la vue d'ensemble : trois régions (Internet, Ailleurs,
// Chez vous), un couloir par zone, des cartes de machine avec leurs services.
// Les données fictives du serveur donnent la carte réelle ; les cas qu'elles ne
// montrent pas (la forme d'un vrai réseau, tout à plat, quarante machines) sont
// servis dans la forme exacte de GET /api/network, figée par le serveur
// (internal/server/testdata/network_*.golden.json). La déduction, elle, est
// prouvée côté serveur (internal/netmap, api_network_map_test.go).

async function serve(page: Page, map: NetworkMap): Promise<void> {
  await page.route("**/api/network", (route) => route.fulfill({ json: map }));
}

function card(page: Page, name: string) {
  return page.locator(".netmap-box", { has: page.locator(".netmap-box-name", { hasText: new RegExp(`^${name}$`) }) });
}

test("la carte réelle : Internet, ailleurs, chez vous, chaque machine en carte dans sa zone", async ({ page, findings }) => {
  findings.where = "/ (carte Réseau)";
  await page.goto("/");
  const map = page.locator(".netmap");
  await expect(map).toBeVisible();
  await expect(map.locator(".netmap-internet")).toContainText(label("network.internet"));
  await expect(map.locator(".netmap-outside")).toContainText("vps-paris-1");
  await expect(map.locator(".netmap-home .netmap-lane").first()).toBeVisible();
  await expect(map.locator(".netmap-home .netmap-box").first()).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("la forme d'un vrai réseau : le VPS de type inconnu est ailleurs, le pare-feu est un bandeau, les services sont dans les cartes", async ({ page, findings }) => {
  findings.where = "/ (carte, forme réelle)";
  await serve(page, demoMap());
  await page.goto("/");
  const outside = page.locator(".netmap-outside");
  const home = page.locator(".netmap-home");
  await expect(outside.locator(".netmap-box")).toHaveCount(1);
  await expect(outside).toContainText("vps-relais");
  await expect(outside.locator(".netmap-chip")).toHaveCount(1);
  await expect(home).not.toContainText("vps-relais");
  // Le pare-feu : un bandeau, pas une colonne d'interfaces ; ni boucle locale ni liste de zones sans machine.
  await expect(home.locator(".netmap-firewall")).toHaveCount(1);
  await expect(home.locator(".netmap-firewall")).toContainText(label("network.map_wan"));
  await expect(home.locator(".netmap-firewall")).not.toContainText("lo0");
  await expect(home.locator(".netmap-interface")).toHaveCount(0);
  // Un couloir par zone qui contient une machine, une carte par machine.
  await expect(home.locator(".netmap-lane")).toHaveCount(6);
  await expect(home.locator(".netmap-box")).toHaveCount(12);
  // Le Pi, sans service, est une carte comme les autres, dans sa zone.
  await expect(card(page, "pi")).toContainText(label("network.map_no_service"));
  await expect(page.locator(".netmap-lane", { hasText: "BACKUP" }).locator(".netmap-box")).toHaveCount(1);
  // Aucun hôte replié, aucun bouton pour replier : la carte est petite.
  await expect(page.locator(".netmap-box.is-folded")).toHaveCount(0);
  await expect(page.locator(".netmap-fold")).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("les traits : un tunnel nommé du VPS au bandeau, les entrées d'Internet, rien de l'interface vers l'hôte", async ({ page, findings }) => {
  findings.where = "/ (traits de la carte)";
  await serve(page, demoMap());
  await page.goto("/");
  await expect(page.locator(".netmap-edge.tunnel")).toHaveCount(1);
  await expect(page.locator(".netmap-edge.entry")).toHaveCount(2);
  await expect(page.locator(".netmap-edge.zone")).toHaveCount(0);
  await expect(page.locator(".netmap-edge.link")).toHaveCount(4);
  await page.locator(".netmap-edge.tunnel .netmap-hit").hover({ force: true });
  await expect(page.locator(".netmap-tip")).toContainText(label("link.proof_observed"));
  expect(findings.unknown()).toEqual([]);
});

test("le survol d'un hôte met ses invités en évidence", async ({ page, findings }) => {
  findings.where = "/ (survol d'un hôte)";
  await serve(page, demoMap());
  await page.goto("/");
  await expect(page.locator(".netmap-box.is-lit")).toHaveCount(0);
  await card(page, "pve").hover();
  await expect(page.locator(".netmap-box.is-lit")).toHaveCount(11);
  await expect(card(page, "vault")).toHaveClass(/is-lit/);
  await expect(card(page, "pi")).toHaveClass(/is-dim/);
  await page.mouse.move(0, 0);
  await expect(page.locator(".netmap-box.is-lit")).toHaveCount(0);
  // « héberge 10 machines » fige la mise en évidence ; un second clic la lève.
  const hosting = card(page, "pve").locator(".netmap-hosting");
  await expect(hosting).toContainText(label("network.map_hosts").replace("%s", "10"));
  await hosting.click();
  await page.mouse.move(0, 0);
  await expect(page.locator(".netmap-box.is-lit")).toHaveCount(11);
  await hosting.click();
  await page.mouse.move(0, 0);
  await expect(page.locator(".netmap-box.is-lit")).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("« VM sur pve » mène à l'hôte et met ses invités en évidence", async ({ page, findings }) => {
  findings.where = "/ (VM sur son hôte)";
  await serve(page, demoMap());
  await page.goto("/");
  const onHost = card(page, "vault").locator(".netmap-host-link");
  await expect(onHost).toHaveText(label("network.map_on_host").replace("%s", label("machine.kind_vm")).replace("%s", "pve"));
  await onHost.click();
  await expect(page.locator(".netmap-box.is-lit")).toHaveCount(11);
  await expect(card(page, "pve")).toHaveClass(/is-lit/);
  await expect(card(page, "dns").locator(".netmap-host-link")).toHaveText(label("network.map_on_host").replace("%s", label("machine.kind_lxc")).replace("%s", "pve"));
  expect(findings.unknown()).toEqual([]);
});

test("les zones sans machine se replient en une puce qui se déplie au clic", async ({ page, findings }) => {
  findings.where = "/ (zones sans machine)";
  await serve(page, demoMap());
  await page.goto("/");
  const toggle = page.getByRole("button", { name: label("network.map_empty_zones").replace("%s", "4") });
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator(".netmap-empty-zone")).toHaveCount(0);
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await expect(page.locator(".netmap-empty-zone")).toHaveCount(4);
  await expect(page.locator(".netmap-empty-list")).toContainText("RESCUE");
  await expect(page.locator(".netmap-lane", { hasText: "RESCUE" })).toHaveCount(0);
  await toggle.click();
  await expect(page.locator(".netmap-empty-zone")).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("une entrée d'Internet nomme son service ; un clic le met en évidence dans la carte de sa machine", async ({ page, findings }) => {
  findings.where = "/ (entrée d'Internet)";
  await serve(page, demoMap());
  await page.goto("/");
  const entries = page.locator(".netmap-internet .netmap-entry");
  await expect(entries).toHaveCount(2);
  const traefik = entries.filter({ hasText: "traefik" });
  await expect(traefik).toContainText(label("network.map_entry_ports").replace("%s", "443"));
  await expect(traefik).toContainText(label("network.map_entry_seen_short"));
  await expect(entries.filter({ hasText: "relais" })).toContainText(label("network.map_entry_ports").replace("%s", "80, 443"));
  await expect(page.locator(".netmap-chip.is-pointed")).toHaveCount(0);
  await traefik.locator("button").click();
  await expect(traefik.locator("button")).toHaveAttribute("aria-pressed", "true");
  await expect(card(page, "proxy").locator(".netmap-chip.is-pointed")).toContainText("traefik");
  await entries.filter({ hasText: "relais" }).locator("button").click();
  await expect(page.locator(".netmap-chip.is-pointed")).toHaveCount(1);
  await expect(page.locator(".netmap-outside .netmap-chip.is-pointed")).toContainText("relais");
  expect(findings.unknown()).toEqual([]);
});

test("un lien dit sa preuve au clavier, et la liste des liens la redit", async ({ page, findings }) => {
  findings.where = "/ (liens de la carte)";
  await serve(page, demoMap());
  await page.goto("/");
  await expect(page.locator(".netmap-edge.link")).toHaveCount(4);
  await page.locator(".netmap-edge.link.proof-declared").first().focus();
  await expect(page.locator(".netmap-tip")).toContainText(label("link.proof_declared"));
  await page.locator(".netmap-wrap summary", { hasText: label("network.map_links_title").replace("%s", "4") }).click();
  const rows = page.locator(".netmap-wrap tbody tr");
  await expect(rows).toHaveCount(4);
  await expect(rows.filter({ hasText: label("network.map_link_proposed") })).toHaveCount(1);
  expect(findings.unknown()).toEqual([]);
});

test("sans Proxmox, sans pare-feu, sans VPS : des machines à plat, des couloirs de sous-réseau", async ({ page, findings }) => {
  findings.where = "/ (carte à plat)";
  await serve(page, flatMap());
  await page.goto("/");
  await expect(page.locator(".netmap-outside")).toHaveCount(0);
  await expect(page.locator(".netmap-firewall")).toHaveCount(0);
  await expect(page.locator(".netmap-hosting")).toHaveCount(0);
  await expect(page.locator(".netmap-lane", { hasText: "10.2.1.0/24" }).locator(".netmap-box")).toHaveCount(2);
  await expect(card(page, "nas")).toContainText(label("network.map_no_service"));
  expect(findings.unknown()).toEqual([]);
});

test("à quarante machines, les hôtes sans défaut s'ouvrent repliés, celui qui a un défaut reste ouvert", async ({ page, findings }) => {
  findings.where = "/ (quarante machines)";
  await serve(page, bigMap());
  await page.goto("/");
  await expect(page.locator(".netmap-box.is-host.is-folded")).toHaveCount(2);
  await expect(page.locator(".netmap-box.is-host:not(.is-folded)")).toHaveCount(1);
  await expect(page.locator(".netmap-box", { hasText: "calme-1-vm1" })).toHaveCount(0);
  await page.getByRole("button", { name: label("network.map_unfold").replace("%s", "calme-1") }).click();
  await expect(page.locator(".netmap-box.is-host.is-folded")).toHaveCount(1);
  await expect(page.locator(".netmap-box", { hasText: "calme-1-vm1" }).first()).toBeVisible();
  await page.getByRole("button", { name: label("network.map_fold_all"), exact: true }).click();
  await expect(page.locator(".netmap-box.is-host.is-folded")).toHaveCount(3);
  await page.getByRole("button", { name: label("network.map_unfold_all"), exact: true }).click();
  await expect(page.locator(".netmap-box.is-host.is-folded")).toHaveCount(0);
  // Un service en défaut mène à sa fiche (les identifiants de cette carte sont fictifs : on lit l'adresse sans la suivre).
  await expect(page.locator(".netmap-chip.state-degraded").first()).toHaveAttribute("href", /\/(services|machines)\//);
  expect(findings.unknown()).toEqual([]);
});
