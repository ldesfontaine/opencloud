import type { Page } from "@playwright/test";

// La première configuration, simulée côté navigateur : le serveur des données
// fictives a déjà un compte. Le jeton et les commandes sont inventés (aucun
// ne vaut quoi que ce soit) ; l'écran, lui, est le vrai.
export async function mockFirstSetup(page: Page, address: { local: boolean; suggestion: string } = { local: false, suggestion: "" }): Promise<void> {
  await page.route("**/api/auth", (route) =>
    route.fulfill({ json: { mode: "none", username: "", enabled_at: null, password_min_length: 12, allowed_users: null } }),
  );
  await page.route("**/api/auth/actions/enable", (route) => route.fulfill({ status: 204 }));
  await page.route("**/api/machines/local-enrollment", (route) =>
    route.fulfill({
      status: 201,
      json: {
        name: "opencloud",
        token: "oc_exemple_de_jeton_inventé_0000",
        steps: [
          { id: "prepare", command: "useradd --system --home-dir /var/lib/opencloud-agent --shell /usr/sbin/nologin opencloud-agent\ninstall -d -m 0700 -o opencloud-agent -g opencloud-agent /var/lib/opencloud-agent" },
          { id: "enroll", command: "printf '%s\\n' 'oc_exemple_de_jeton_inventé_0000' | sudo -u opencloud-agent /var/lib/opencloud-agent/bin/current agent -server https://opencloud.exemple.fr" },
          { id: "unit", command: "cat > /etc/systemd/system/opencloud-agent.service <<'EOF'\n[Unit]\nDescription=openCloud (agent)\n[Service]\nUser=opencloud-agent\nEOF\nsystemctl daemon-reload\nsystemctl enable --now opencloud-agent.service" },
        ],
        proxmox_commands: "",
        expires_at: new Date(Date.now() + 3_600_000).toISOString(),
        url_local: address.local,
        url_suggestion: address.suggestion,
      },
    }),
  );
}

// Crée le compte du formulaire de première configuration.
export async function createFirstAccount(page: Page): Promise<void> {
  await page.locator("#auth-username").fill("admin");
  await page.locator("#auth-password").fill("une-phrase-de-passe-de-test");
  await page.locator("#auth-confirm").fill("une-phrase-de-passe-de-test");
  await page.locator("form button[type=submit]").click();
}
