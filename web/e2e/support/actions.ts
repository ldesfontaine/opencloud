import type { Locator, Page, Request } from "@playwright/test";

import { label } from "./catalog";
import { exploreDialog, exerciseTips } from "./dialogs";
import type { Findings } from "./findings";
import { exerciseSelects } from "./selects";
import { pageLoadTimeout } from "./timeouts";

// Ce qu'un clic peut déclencher : un bouton de la page, ou un choix du menu
// « … » d'un bouton.
interface Action {
  name: string;
  // Le rang du bouton parmi ceux du même nom : un geste (« Marquer comme vue »)
  // change la page, les rangs d'avant ne valent plus, les noms si.
  rank: number;
  opener: number;
  item: number | null;
}

// Les boutons de la zone principale : ni les menus déroulants (ils ont leur
// parcours), ni les ouvreurs de menu « … » (leurs choix sont des actions à
// part), ni ceux d'une fenêtre ou d'un menu ouverts.
function pageButtons(page: Page): Locator {
  return page.locator("main button:visible:not([disabled]):not([role=combobox]):not([aria-haspopup=menu]):not([role=menuitem]), main a.btn:visible");
}

function menuOpeners(page: Page): Locator {
  return page.locator("main button[aria-haspopup=menu]:visible:not([disabled])");
}

async function nameOf(control: Locator): Promise<string> {
  const text = ((await control.innerText().catch(() => "")) || (await control.getAttribute("aria-label")) || "").trim().replace(/\s+/g, " ");
  return text.slice(0, 50) || "(sans nom)";
}

async function discoverActions(page: Page): Promise<Action[]> {
  const actions: Action[] = [];
  const buttons = pageButtons(page);
  const ranks = new Map<string, number>();
  for (let index = 0; index < Math.min(await buttons.count(), 60); index++) {
    const name = await nameOf(buttons.nth(index));
    const rank = ranks.get(name) ?? 0;
    ranks.set(name, rank + 1);
    actions.push({ name, rank, opener: index, item: null });
  }
  const openers = menuOpeners(page);
  for (let opener = 0; opener < Math.min(await openers.count(), 12); opener++) {
    await openers.nth(opener).click();
    const items = page.locator("[role=menu] [role=menuitem]:not([aria-disabled=true])");
    const owner = await nameOf(openers.nth(opener));
    for (let item = 0; item < (await items.count()); item++) {
      actions.push({ rank: 0, name: `menu « ${owner} » › ${await nameOf(items.nth(item))}`, opener, item });
    }
    await page.keyboard.press("Escape");
  }
  return actions;
}

// Le bouton du même nom et du même rang ; null s'il a disparu, parce qu'un
// geste d'avant l'a consommé (une alerte acquittée n'a plus son bouton).
async function findButton(page: Page, action: Action): Promise<Locator | null> {
  const buttons = pageButtons(page);
  let rank = 0;
  for (let index = 0; index < (await buttons.count()); index++) {
    if ((await nameOf(buttons.nth(index))) === action.name) {
      if (rank === action.rank) {
        return buttons.nth(index);
      }
      rank++;
    }
  }
  return null;
}

async function perform(page: Page, action: Action): Promise<void> {
  if (action.item === null) {
    const target = await findButton(page, action);
    if (target === null) {
      return;
    }
    await target.click();
    return;
  }
  await menuOpeners(page).nth(action.opener).click();
  await page.locator("[role=menu] [role=menuitem]:not([aria-disabled=true])").nth(action.item).click();
}

// Les lectures de l'API en vol par page. Un flux (EventSource) ne s'achève
// jamais : il ne compte pas.
const requestsInFlight = new WeakMap<Page, Set<Request>>();

function watchRequests(page: Page): Set<Request> {
  const known = requestsInFlight.get(page);
  if (known !== undefined) {
    return known;
  }
  const inFlight = new Set<Request>();
  const isRead = (request: Request) => request.resourceType() !== "eventsource" && new URL(request.url()).pathname.startsWith("/api/");
  page.on("request", (request) => isRead(request) && inFlight.add(request));
  page.on("requestfinished", (request) => inFlight.delete(request));
  page.on("requestfailed", (request) => inFlight.delete(request));
  requestsInFlight.set(page, inFlight);
  return inFlight;
}

// La page est posée quand ses lectures sont revenues et qu'aucune n'en a
// déclenché d'autre : une fenêtre d'adresse (/sauvegardes/cibles/nouvelle)
// ne s'ouvre qu'une fois ses données là, bien après que « main » se voit.
async function waitForSettledPage(page: Page, inFlight: Set<Request>): Promise<void> {
  const deadline = Date.now() + pageLoadTimeout;
  do {
    while (inFlight.size > 0) {
      if (Date.now() > deadline) {
        throw new Error(`la page ne se pose pas : ${inFlight.size} lecture(s) en cours`);
      }
      await page.waitForTimeout(50);
    }
    // Le rendu qui suit une réponse peut en déclencher une autre.
    await page.waitForTimeout(150);
  } while (inFlight.size > 0);
}

async function openPage(page: Page, path: string): Promise<void> {
  const inFlight = watchRequests(page);
  await page.goto(path, { timeout: pageLoadTimeout });
  await page.locator("main").waitFor({ state: "visible", timeout: pageLoadTimeout });
  await waitForSettledPage(page, inFlight);
}

// Un geste qui exige l'agent est, machine hors ligne, désactivé avec sa
// raison à côté (AgentGate) : chaque porte qui montre la raison grise tous ses
// boutons.
async function checkGates(page: Page, findings: Findings): Promise<void> {
  const reason = label("agent.offline_reason");
  const gates = page.locator("main .agent-gate");
  for (let index = 0; index < (await gates.count()); index++) {
    const gate = gates.nth(index);
    if ((await gate.locator(".agent-gate-reason", { hasText: reason }).count()) === 0) {
      continue;
    }
    findings.done.gates++;
    const buttons = gate.locator("button");
    for (let button = 0; button < (await buttons.count()); button++) {
      if (!(await buttons.nth(button).isDisabled())) {
        findings.add(`un geste dit « ${reason} » mais son bouton « ${await nameOf(buttons.nth(button))} » reste actif`);
      }
    }
  }
}

// Parcourt un écran : ses menus déroulants, ses infobulles, puis chaque
// bouton et chaque choix de menu « … », un par un, sur une page rechargée.
// Ce qui ouvre une fenêtre l'explore et la referme. Une adresse de fenêtre
// (/machines/nouvelle) s'ouvre sur sa fenêtre : c'est elle qu'on explore, le
// fond est inerte.
export async function exerciseScreen(page: Page, path: string, findings: Findings): Promise<void> {
  findings.where = path;
  findings.done.screens++;
  findings.screens.push(path);
  await openPage(page, path);
  if ((await page.locator("dialog[open]").count()) > 0) {
    await exploreDialog(page, findings);
    return;
  }
  await checkGates(page, findings);
  await exerciseSelects(page, page.locator("main"), findings);
  await exerciseTips(page.locator("main"), findings);
  await openPage(page, path);
  const actions = await discoverActions(page);
  for (const action of actions) {
    findings.where = `${path} › ${action.name}`;
    await openPage(page, path);
    try {
      await perform(page, action);
    } catch (error) {
      findings.add(`le bouton ne se clique pas : ${error instanceof Error ? error.message : String(error)}`);
      continue;
    }
    if (action.item === null) {
      findings.done.buttons++;
    } else {
      findings.done.menuChoices++;
    }
    await page.waitForTimeout(150);
    if ((await page.locator("dialog[open]").count()) > 0) {
      await exploreDialog(page, findings);
    }
  }
}

// Les adresses internes qu'une page propose, hors fenêtres et ancres.
export async function linksOf(page: Page, path: string): Promise<string[]> {
  await openPage(page, path);
  await page.waitForTimeout(400);
  const hrefs = await page.locator("a[href^='/']").evaluateAll((anchors) => anchors.map((anchor) => anchor.getAttribute("href") ?? ""));
  return hrefs.map((href) => href.split("#")[0] ?? "").filter((href) => href !== "" && !href.startsWith("/api/") && !href.startsWith("/statut/") && !href.startsWith("/agent"));
}
