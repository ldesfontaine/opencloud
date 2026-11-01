import { mkdirSync } from "node:fs";

import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// Les assistants Pare-feu et Proxmox, de l'adresse au récapitulatif : « Tester
// l'accès » (adresse joignable ou non), l'empreinte relevée par le serveur et
// confirmée d'un clic, la saisie à la main en repli, la clé masquée au
// récapitulatif et sur la fiche. Le faux OPNsense des données fictives (celui
// du pare-feu déjà déclaré) sert d'adresse joignable au certificat auto-signé.
// Avec E2E_CAPTURES=1, il laisse aussi les captures de U4 (clair et sombre).

const captureFolder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url).pathname;
const capturing = process.env.E2E_CAPTURES === "1";

// La paire du faux OPNsense (opnsensetest.Key et Secret) : fausse, de test.
const fakeKey = "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789testkey"; // gitleaks:allow
const fakeSecret = "ZyXwVuTsRqPoNmLkJiHgFeDcBa9876543210secret"; // gitleaks:allow

const colons = (hex: string) => (hex.toUpperCase().match(/.{2}/g) ?? []).join(":");

async function declaredFirewall(page: Page): Promise<{ url: string; pin: string }> {
  const answer = await page.request.get("/api/firewalls");
  const body = (await answer.json()) as { firewalls: { api_url: string; pin: string }[] };
  const first = body.firewalls[0];
  if (first === undefined) {
    throw new Error("le jeu de données n'a pas de pare-feu");
  }
  return { url: first.api_url, pin: first.pin };
}

function shooter(page: Page, theme: "light" | "dark") {
  const suffix = theme === "light" ? "clair" : "sombre";
  return async (name: string) => {
    if (capturing) {
      mkdirSync(captureFolder, { recursive: true });
      await page.screenshot({ path: `${captureFolder}U4-${name}-${suffix}.png` });
    }
  };
}

for (const theme of ["light", "dark"] as const) {
  test(`pare-feu : tester l'accès, confirmer l'empreinte, clé masquée, ${theme}`, async ({ page, findings }) => {
    findings.where = "/pare-feu/nouveau";
    await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
    const shoot = shooter(page, theme);
    const firewall = await declaredFirewall(page);

    await page.goto("/pare-feu/nouveau");
    const dialog = page.locator("dialog[open]");
    await expect(dialog).toBeVisible();
    const next = dialog.getByRole("button", { name: label("wizard.next"), exact: true });
    const test_ = dialog.getByRole("button", { name: label("certificate.test"), exact: true });

    // 1. L'adresse : le bouton attend une adresse https, puis dit pourquoi elle ne répond pas.
    await dialog.locator("#firewall-name").fill(`OPNsense du salon ${theme}`);
    await expect(test_).toBeDisabled();
    await dialog.locator("#firewall-url").fill("https://127.0.0.1:1");
    await test_.click();
    await expect(dialog.locator(".note-danger")).toContainText(label("certificate.cause_refused"));
    await shoot("pare-feu-adresse-injoignable");

    // Puis l'adresse du faux OPNsense : joignable, certificat à confirmer.
    await dialog.locator("#firewall-url").fill(firewall.url);
    await expect(dialog.locator(".note-danger")).toHaveCount(0);
    await test_.click();
    await expect(dialog.locator(".note-warn")).toContainText("Son certificat n'est pas reconnu");
    await shoot("pare-feu-test-acces");
    await next.click();

    // 2. Les gestes : menus dans la langue de l'interface, un seul privilège DHCP à choisir, rien d'ISC.
    await expect(dialog.locator(".wizard-title")).toHaveText(label("firewall.step_prepare"));
    await expect(dialog.getByText(label("firewall.prepare_intro").replace("{product}", "openCloud"))).toBeVisible();
    await expect(dialog.locator("a.external-link").first()).toContainText(label("firewall.menu_users"));
    await expect(dialog.getByText(label("firewall.gesture_leases_text"))).toBeVisible();
    await expect(dialog.getByText(/\bISC\b/)).toHaveCount(0);
    await dialog.locator(".checklist-item").nth(1).scrollIntoViewIfNeeded();
    await shoot("pare-feu-privileges");
    await next.click();

    // 3. Clé et secret.
    await dialog.locator("#firewall-key").fill(fakeKey);
    await dialog.locator("#firewall-secret").fill(fakeSecret);
    await next.click();

    // 4. Certificat : le serveur a relevé l'empreinte, l'opérateur confirme d'un clic.
    await expect(dialog.locator(".wizard-title")).toHaveText(label("firewall.step_certificate"));
    await expect(dialog.locator(".token-box code")).toHaveText(colons(firewall.pin));
    await expect(dialog.getByRole("button", { name: label("certificate.confirm"), exact: true })).toBeVisible();
    await shoot("pare-feu-empreinte");
    await dialog.getByRole("button", { name: label("certificate.confirm"), exact: true }).click();
    await expect(dialog.getByText(label("certificate.confirmed"))).toBeVisible();
    // Le repli : la saisie à la main porte la même empreinte, en hexadécimal.
    await dialog.getByText(label("certificate.manual"), { exact: true }).click();
    await expect(dialog.locator("#firewall-pin")).toHaveValue(firewall.pin);
    await next.click();

    // 5. Récapitulatif : la clé n'y est jamais en entier, le secret non plus.
    await expect(dialog.locator(".wizard-summary")).toBeVisible();
    await expect(dialog.locator(".wizard-summary")).toContainText("AbCd…");
    await expect(dialog.locator(".wizard-summary")).not.toContainText(fakeKey);
    await expect(dialog.locator(".wizard-summary")).toContainText(colons(firewall.pin));
    await shoot("pare-feu-recapitulatif");

    // Déclarer pour de bon : la fiche ne relit que le début de la clé.
    await dialog.getByRole("button", { name: label("firewall.submit_new"), exact: true }).click();
    await expect(page).toHaveURL(/\/pare-feu\/[0-9a-f]+$/);
    await expect(page.locator("main")).toContainText("AbCd…");
    expect(await page.locator("main").innerText()).not.toContain(fakeKey);
    await shoot("pare-feu-fiche");
    expect(findings.unknown()).toEqual([]);
  });

  test(`pare-feu : modifier redemande la clé, ${theme}`, async ({ page, findings }) => {
    findings.where = "/pare-feu/:id/modifier";
    await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
    const list = (await (await page.request.get("/api/firewalls")).json()) as { firewalls: { id: string }[] };
    const id = list.firewalls[0]?.id ?? "";
    await page.goto(`/pare-feu/${id}/modifier`);
    const dialog = page.locator("dialog[open]");
    await expect(dialog.locator(".wizard-summary")).toBeVisible();
    // La clé enregistrée n'est pas relue : le champ est vide, son début est rappelé.
    await dialog.getByRole("button", { name: label("wizard.previous"), exact: true }).click();
    await dialog.getByRole("button", { name: label("wizard.previous"), exact: true }).click();
    await expect(dialog.locator("#firewall-key")).toHaveValue("");
    await expect(dialog.getByText("AbCd…")).toBeVisible();
    expect(findings.unknown()).toEqual([]);
  });

  test(`proxmox : tester l'accès, confirmer l'empreinte, identifiant du jeton masqué, ${theme}`, async ({ page, findings }) => {
    findings.where = "/machines/:id/proxmox";
    await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
    const shoot = shooter(page, theme);
    const firewall = await declaredFirewall(page);
    const machines = (await (await page.request.get("/api/machines")).json()) as { machines: { id: string }[] };
    const id = machines.machines.find((machine) => machine.id !== "local")?.id ?? "";

    await page.goto(`/machines/${id}/proxmox`);
    const dialog = page.locator("dialog[open]");
    await expect(dialog).toBeVisible();
    const next = dialog.getByRole("button", { name: label("wizard.next"), exact: true });

    // L'adresse du faux serveur sert ici seulement de pair TLS à auto-signé.
    await dialog.locator("#proxmox-url").fill(firewall.url);
    await dialog.getByRole("button", { name: label("certificate.test"), exact: true }).click();
    await expect(dialog.locator(".note-warn")).toContainText("Son certificat n'est pas reconnu");
    await dialog.locator("#proxmox-token-id").fill("lecteur@pve!lecture");
    await dialog.locator("#proxmox-secret").fill("secret-de-test-12345");
    await shoot("proxmox-acces");
    await next.click();

    await expect(dialog.locator(".token-box code")).toHaveText(colons(firewall.pin));
    await dialog.getByRole("button", { name: label("certificate.confirm"), exact: true }).click();
    await expect(dialog.getByText(label("certificate.confirmed"))).toBeVisible();
    await shoot("proxmox-empreinte");
    await next.click();

    await expect(dialog.locator(".wizard-summary")).toContainText("lect…");
    await expect(dialog.locator(".wizard-summary")).not.toContainText("lecteur@pve!lecture");
    await shoot("proxmox-recapitulatif");
    expect(findings.unknown()).toEqual([]);
  });
}
