import { mkdirSync } from "node:fs";

import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// La fiche d'un pare-feu : l'état en un coup d'œil, les interfaces en cartes, les
// règles groupées avec leur recherche et leurs filtres, les baux rattachés aux
// machines, la connexion repliée (clé jamais en entier), puis la grille « qui
// parle à qui ». Chaque bouton est cliqué, chaque filtre pris, chaque fenêtre
// ouverte. Données fictives : le faux OPNsense des bancs, en quatre zones.
// Avec E2E_CAPTURES=1, il laisse aussi les captures de U5 (clair et sombre).

const captureFolder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url).pathname;
const capturing = process.env.E2E_CAPTURES === "1";

// La paire du faux OPNsense (opnsensetest.Key) : fausse, de test.
const fakeKey = "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789testkey"; // gitleaks:allow

async function firewallPath(page: Page): Promise<string> {
  const body = (await (await page.request.get("/api/firewalls")).json()) as { firewalls: { id: string }[] };
  const first = body.firewalls[0];
  if (first === undefined) {
    throw new Error("le jeu de données n'a pas de pare-feu");
  }
  return `/pare-feu/${first.id}`;
}

function shooter(page: Page, theme: "light" | "dark") {
  const suffix = theme === "light" ? "clair" : "sombre";
  // Une capture montre toute la page : une section sous la ligne de flottaison
  // (baux, groupe de règles déplié, filtre actif) n'apparaîtrait pas autrement.
  // Seules les fenêtres (modifier, retirer) se prennent en vue normale.
  return async (name: string, fullPage = true) => {
    if (capturing) {
      mkdirSync(captureFolder, { recursive: true });
      // La barre latérale est collante : une page défilée la décale dans une
      // capture entière. On revient en haut, comme un lecteur qui la regarde.
      await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));
      await page.waitForFunction(() => window.scrollY === 0);
      await page.screenshot({ path: `${captureFolder}U5-pare-feu-${name}-${suffix}.png`, fullPage });
    }
  };
}

for (const theme of ["light", "dark"] as const) {
  test(`pare-feu : la fiche visuelle, ${theme}`, async ({ page, findings }) => {
    await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
    const shoot = shooter(page, theme);
    const path = await firewallPath(page);
    findings.where = path;
    await page.goto(path);

    // L'état en un coup d'œil : quatre tuiles, chacune mène à son bloc.
    const tiles = page.locator(".firewall-stats a.stat-link");
    await expect(tiles).toHaveCount(4);
    await expect(tiles.nth(0)).toContainText("OPNsense 25.1.6");
    await expect(tiles.nth(2)).toContainText(label("firewall.glance_gateways"));
    await expect(tiles.nth(2)).toContainText("1"); // BACKUP est coupée
    await expect(tiles.nth(3)).toContainText(label("firewall.glance_rules"));
    await shoot("fiche");
    await tiles.nth(3).click();
    await expect(page).toHaveURL(/#regles$/);
    await tiles.nth(2).click();
    await expect(page).toHaveURL(/#passerelles$/);
    await expect(page.locator("#passerelles .firewall-gateway")).toHaveCount(3);

    // Les interfaces en cartes ; une carte ouvre le groupe de règles de sa zone.
    const cards = page.locator(".firewall-zone");
    await expect(cards).toHaveCount(4);
    await expect(page.locator(".firewall-zone-up")).toHaveCount(2);
    await expect(page.locator(".firewall-zone-down")).toHaveCount(1);
    await expect(page.locator(".firewall-zone-other")).toHaveCount(1);
    const lan = page.locator("details.firewall-group#regles-lan");
    await expect(lan).not.toHaveAttribute("open", "");
    await cards.filter({ hasText: "LAN" }).click();
    await expect(lan).toHaveAttribute("open", "");
    await expect(lan.locator("tbody tr")).toHaveCount(5);
    await shoot("regles-groupe");

    // Un groupe s'ouvre et se referme au clic sur son titre.
    await lan.locator("summary").click();
    await expect(lan).not.toHaveAttribute("open", "");
    await page.locator("details.firewall-group#regles-wan summary").click();
    await expect(page.locator("details.firewall-group#regles-wan")).toHaveAttribute("open", "");

    // La recherche ouvre les groupes qui répondent et cache les autres.
    const search = page.getByRole("searchbox", { name: label("firewall.rules_search") });
    await search.fill("dns");
    await expect(page.locator("details.firewall-group")).toHaveCount(1);
    await expect(page.locator("details.firewall-group[open] tbody tr")).toHaveCount(1);
    await search.fill("zzzzzz");
    await expect(page.getByText(label("firewall.rules_no_match"))).toBeVisible();
    await page.getByRole("button", { name: label("firewall.rules_clear"), exact: true }).click();
    await expect(search).toHaveValue("");
    await expect(page.locator("details.firewall-group")).toHaveCount(4);

    // Les trois filtres, chacun avec son compte.
    const kinds = page.getByRole("radiogroup", { name: label("firewall.rules_kind") });
    await kinds.getByRole("radio", { name: new RegExp(`^${label("firewall.rules_filter_block")}`) }).check();
    await expect(page.locator("details.firewall-group")).toHaveCount(3);
    await expect(page.locator("details.firewall-group tbody tr")).toHaveCount(3);
    await shoot("regles-bloquantes");
    await kinds.getByRole("radio", { name: new RegExp(`^${label("firewall.rules_filter_off")}`) }).check();
    await expect(page.locator("details.firewall-group")).toHaveCount(1);
    await expect(page.locator(".firewall-off")).toHaveCount(1);
    await kinds.getByRole("radio", { name: new RegExp(`^${label("firewall.rules_filter_pass")}`) }).check();
    await expect(page.locator("details.firewall-group tbody tr")).toHaveCount(7);
    await kinds.getByRole("radio", { name: new RegExp(`^${label("firewall.rules_filter_all")}`) }).check();

    // Les baux : celui d'une machine d'openCloud porte son nom, avec un lien.
    const known = page.locator("a.firewall-machine");
    await expect(known).toHaveCount(1);
    await expect(page.locator("tr", { hasText: "imprimante" })).not.toContainText("vps-lyon");
    await known.scrollIntoViewIfNeeded();
    await shoot("baux");
    await known.click();
    await expect(page).toHaveURL(/\/machines\/[^/]+$/);
    await page.goBack();

    // La connexion est repliée ; ouverte, la clé n'est jamais entière.
    const connection = page.locator("#connexion details");
    await expect(connection).not.toHaveAttribute("open", "");
    await connection.locator("summary").click();
    await expect(connection).toHaveAttribute("open", "");
    await expect(page.locator("#connexion")).toContainText(fakeKey.slice(0, 4));
    expect(await page.content()).not.toContain(fakeKey);
    await shoot("connexion");

    // La grille « qui parle à qui ».
    await page.getByRole("link", { name: label("firewall.tab_flows"), exact: true }).click();
    await expect(page).toHaveURL(/\/flux$/);
    const grid = page.locator("table.firewall-grid");
    await expect(grid).toBeVisible();
    // LAN → pare-feu : DNS passe ; DMZ → LAN : refus en toutes lettres.
    const lanToSelf = grid.locator("td", { has: page.getByText("DNS") }).first();
    await expect(lanToSelf).toContainText("UDP 53");
    await expect(grid.locator(".firewall-line-deny")).toHaveCount(3);
    // Une case sans règle est refusée, et le dit à qui ne la voit pas.
    const refused = grid.locator("td.firewall-cell-empty").first();
    await expect(refused).toHaveAttribute("aria-label", new RegExp(label("firewall.flow_refused")));
    await expect(grid.locator("td.firewall-cell-same")).not.toHaveCount(0);
    await expect(page.getByText(label("firewall.flow_note"))).toBeVisible();
    // La règle flottante n'est placée nulle part : elle est à part, repliée.
    const unplaced = page.locator("details.fold", { hasText: /Règles non placées/ });
    await unplaced.locator("summary").click();
    await expect(unplaced).toHaveAttribute("open", "");
    await expect(unplaced.locator(".firewall-line")).toHaveCount(1);
    await shoot("flux");
    await page.getByRole("link", { name: label("firewall.tab_overview"), exact: true }).click();
    await expect(page).toHaveURL(/\/pare-feu\/[^/]+$/);

    // Modifier ouvre l'assistant sur la fiche ; Retirer demande confirmation.
    await page.getByRole("link", { name: label("firewall.edit"), exact: true }).click();
    const dialog = page.locator("dialog[open]");
    await expect(dialog).toBeVisible();
    await shoot("modifier", false);
    await dialog.getByRole("button", { name: label("modal.cancel"), exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await page.getByRole("button", { name: label("firewall.remove"), exact: true }).click();
    await expect(page.getByText(/Retirer ce pare-feu/)).toBeVisible();
    await shoot("retirer", false);
    await page.getByRole("button", { name: label("confirm.cancel"), exact: true }).click();
    await expect(page.getByRole("button", { name: label("firewall.remove"), exact: true })).toBeVisible();

    findings.done.screens += 2;
    findings.done.buttons += 12;
    findings.done.dialogs += 1;
  });
}
