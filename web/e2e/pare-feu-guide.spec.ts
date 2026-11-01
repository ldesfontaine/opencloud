import { mkdirSync } from "node:fs";

import { expect, test } from "./support/findings";
import { label } from "./support/catalog";

// L'assistant « Déclarer un pare-feu » : les gestes à faire dans OPNsense, avec
// leurs liens directs construits depuis l'adresse saisie. Chaque case se
// coche, chaque bouton Copier se presse, chaque lien pointe la bonne page.
// Avec E2E_CAPTURES=1, il laisse aussi les captures de F2 (clair et sombre).

const captureFolder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url).pathname;
const capturing = process.env.E2E_CAPTURES === "1";

for (const theme of ["light", "dark"] as const) {
  test(`pare-feu : les gestes OPNsense, ${theme}`, async ({ page, context, findings }) => {
    findings.where = "/pare-feu/nouveau";
    await context.grantPermissions(["clipboard-read", "clipboard-write"]).catch(() => undefined);
    await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
    const suffix = theme === "light" ? "clair" : "sombre";
    const shoot = async (name: string) => {
      if (capturing) {
        mkdirSync(captureFolder, { recursive: true });
        await page.screenshot({ path: `${captureFolder}F2-pare-feu-${name}-${suffix}.png` });
      }
    };

    await page.goto("/pare-feu/nouveau");
    const dialog = page.locator("dialog[open]");
    await expect(dialog).toBeVisible();
    const next = dialog.getByRole("button", { name: label("wizard.next"), exact: true });

    // 1. L'adresse d'abord : sans elle, on n'avance pas.
    await dialog.locator("#firewall-name").fill("OPNsense maison");
    await next.click();
    await expect(dialog.getByRole("alert")).toContainText(label("firewall.url_invalid"));
    await dialog.locator("#firewall-url").fill("https://192.0.2.1:8443");
    await shoot("adresse");
    await next.click();

    // 2. Les gestes : trois cases, chacune avec son lien direct.
    await expect(dialog.locator(".wizard-title")).toHaveText(label("firewall.step_prepare"));
    await expect(dialog.locator(".checklist-item")).toHaveCount(3);
    const links = dialog.locator("a.external-link");
    await expect(links).toHaveCount(3);
    for (const link of await links.all()) {
      await expect(link).toHaveAttribute("href", "https://192.0.2.1:8443/ui/auth/user");
      await expect(link).toHaveAttribute("target", "_blank");
      await expect(link).toHaveAttribute("rel", /noopener/);
    }
    await expect(dialog.locator(".copy-list code")).toHaveText([
      "Lobby: Dashboard",
      "Status: Interfaces",
      "System: Gateways",
      "Firewall: Rules [new]",
      "Services: Dnsmasq DNS/DHCP: Settings",
      "Services: DHCP: Kea(v4)",
    ]);
    for (const copy of await dialog.locator(".copy-list button").all()) {
      await copy.click();
      await expect(copy).toHaveText(new RegExp(`${label("machine.copied")}|${label("machine.copy_failed")}`));
    }
    for (const box of await dialog.locator(".checklist-item input[type=checkbox]").all()) {
      await box.check();
      await expect(box).toBeChecked();
    }
    await dialog.locator(".wizard-title").scrollIntoViewIfNeeded();
    await shoot("gestes");

    // 3. Clé et secret.
    await next.click();
    await expect(dialog.locator(".wizard-title")).toHaveText(label("firewall.step_access"));
    await dialog.locator("#firewall-key").fill("clefdetest123");
    await dialog.locator("#firewall-secret").fill("secretdetest123");
    await shoot("cle-secret");

    // Revenir en arrière garde les cases cochées.
    await dialog.getByRole("button", { name: label("wizard.previous"), exact: true }).click();
    for (const box of await dialog.locator(".checklist-item input[type=checkbox]").all()) {
      await expect(box).toBeChecked();
    }
    await next.click();

    // 4. Certificat, puis le récapitulatif : les étapes sans saisie n'y figurent pas.
    await next.click();
    await next.click();
    await expect(dialog.locator(".wizard-summary")).toBeVisible();
    await expect(dialog.locator(".wizard-summary-step")).toHaveCount(3);
    await shoot("recapitulatif");
    expect(findings.unknown()).toEqual([]);
  });
}

// L'info-bulle du premier champ d'une fenêtre s'ouvre sous le « ? » : au-dessus,
// le corps défilant de la fenêtre la rognerait.
for (const theme of ["light", "dark"] as const) {
  test(`phrase de passe : l'info-bulle du premier champ n'est pas coupée, ${theme}`, async ({ page, findings }) => {
    findings.where = "/sauvegardes/cle";
    await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
    await page.goto("/sauvegardes/cle");
    const dialog = page.locator("dialog[open]");
    await expect(dialog).toBeVisible();
    await dialog.locator(".field-tip-button").first().hover();
    const bubble = dialog.locator(".field-tip-text").first();
    await expect(bubble).toBeVisible();
    const modal = await dialog.boundingBox();
    const box = await bubble.boundingBox();
    expect(box?.y).toBeGreaterThanOrEqual(modal?.y ?? 0);
    if (capturing) {
      await page.screenshot({ path: `${captureFolder}F2-phrase-de-passe-aide-${theme === "light" ? "clair" : "sombre"}.png` });
    }
  });
}
