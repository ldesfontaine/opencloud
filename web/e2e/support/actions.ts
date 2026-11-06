import type { Locator, Page, Request } from "@playwright/test";

import { label } from "./catalog";
import { exploreDialog, exerciseTips } from "./dialogs";
import { expect, type Findings } from "./findings";
import { exerciseSelects } from "./selects";
import { actionTimeout, pageLoadTimeout } from "./timeouts";

// Ce qu'un clic peut déclencher : un bouton de la page, ou un choix du menu
// « … » d'un bouton. Les deux se retrouvent par leur nom et leur rang parmi
// les homonymes, jamais par leur position : la page se charge par morceaux, et
// la position d'une carte ou d'un choix change tant que ses données arrivent.
interface Action {
  name: string;
  // Le rang du bouton parmi ceux du même nom : un geste (« Marquer comme vue »)
  // change la page, les rangs d'avant ne valent plus, les noms si.
  rank: number;
  menu: MenuChoice | null;
}

interface MenuChoice {
  opener: string;
  openerRank: number;
  item: string;
  itemRank: number;
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

function nextRank(ranks: Map<string, number>, name: string): number {
  const rank = ranks.get(name) ?? 0;
  ranks.set(name, rank + 1);
  return rank;
}

async function discoverActions(page: Page): Promise<Action[]> {
  const actions: Action[] = [];
  const buttons = pageButtons(page);
  const buttonRanks = new Map<string, number>();
  for (let index = 0; index < Math.min(await buttons.count(), 60); index++) {
    const name = await nameOf(buttons.nth(index));
    actions.push({ name, rank: nextRank(buttonRanks, name), menu: null });
  }
  const openers = menuOpeners(page);
  const openerRanks = new Map<string, number>();
  for (let opener = 0; opener < Math.min(await openers.count(), 12); opener++) {
    const owner = await nameOf(openers.nth(opener));
    const openerRank = nextRank(openerRanks, owner);
    await openers.nth(opener).click();
    const menu = page.locator("[role=menu]");
    await menu.waitFor({ state: "visible" });
    const items = menuItems(page);
    const itemRanks = new Map<string, number>();
    for (let item = 0; item < (await items.count()); item++) {
      const itemName = await nameOf(items.nth(item));
      actions.push({ name: `menu « ${owner} » › ${itemName}`, rank: 0, menu: { opener: owner, openerRank, item: itemName, itemRank: nextRank(itemRanks, itemName) } });
    }
    await page.keyboard.press("Escape");
  }
  return actions;
}

function menuItems(page: Page): Locator {
  return page.locator("[role=menu] [role=menuitem]:not([aria-disabled=true])");
}

// La place du contrôle du même nom et du même rang ; -1 s'il n'y est pas.
async function indexOfControl(controls: Locator, name: string, rank: number): Promise<number> {
  let seen = 0;
  for (let index = 0; index < (await controls.count()); index++) {
    if ((await nameOf(controls.nth(index))) === name) {
      if (seen === rank) {
        return index;
      }
      seen++;
    }
  }
  return -1;
}

// Attend que le contrôle paraisse : sa carte ou son choix de menu n'existe
// qu'une fois ses données lues.
async function waitForControl(controls: Locator, name: string, rank: number): Promise<Locator> {
  const poll = expect.poll(() => indexOfControl(controls, name, rank), { message: `« ${name} » ne paraît pas`, timeout: actionTimeout });
  await poll.toBeGreaterThanOrEqual(0);
  return controls.nth(await indexOfControl(controls, name, rank));
}

// Le bouton du même nom et du même rang ; null s'il a disparu, parce qu'un
// geste d'avant l'a consommé (une alerte acquittée n'a plus son bouton).
async function findButton(page: Page, action: Action): Promise<Locator | null> {
  const buttons = pageButtons(page);
  const index = await indexOfControl(buttons, action.name, action.rank);
  return index < 0 ? null : buttons.nth(index);
}

async function perform(page: Page, action: Action): Promise<void> {
  if (action.menu === null) {
    const target = await findButton(page, action);
    if (target === null) {
      return;
    }
    await target.click();
    return;
  }
  const opener = await waitForControl(menuOpeners(page), action.menu.opener, action.menu.openerRank);
  await opener.click();
  const item = await waitForControl(menuItems(page), action.menu.item, action.menu.itemRank);
  await item.click();
}

// Les lectures de l'API d'une page : celles en vol, et combien ont démarré.
interface Reads {
  inFlight: Set<Request>;
  started: number;
}

const readsOfPage = new WeakMap<Page, Reads>();

// Un flux (EventSource) ne s'achève jamais : il ne compte pas.
function watchRequests(page: Page): Reads {
  const known = readsOfPage.get(page);
  if (known !== undefined) {
    return known;
  }
  const reads: Reads = { inFlight: new Set(), started: 0 };
  const isRead = (request: Request) => request.resourceType() !== "eventsource" && new URL(request.url()).pathname.startsWith("/api/");
  page.on("request", (request) => {
    if (isRead(request)) {
      reads.inFlight.add(request);
      reads.started++;
    }
  });
  page.on("requestfinished", (request) => reads.inFlight.delete(request));
  page.on("requestfailed", (request) => reads.inFlight.delete(request));
  readsOfPage.set(page, reads);
  return reads;
}

// Deux images peintes : la page a rendu ce que sa dernière réponse a changé et
// lancé les lectures que ce rendu déclenche. La durée suit la charge de la
// machine, ce qu'un délai fixe ne fait pas. Une navigation en cours coupe
// l'attente : la page d'après se mesure à son tour.
async function waitForRenderedFrames(page: Page): Promise<void> {
  await page
    .evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    .catch(() => undefined);
}

// La page est posée quand au moins `startedAtLeast` lectures ont démarré et que
// trois rendus de suite la trouvent sans lecture en vol : une réponse qui en
// déclenche une autre a toujours une lecture en vol à la fin d'un des rendus.
// Une lecture du direct, qui arrive quand le serveur publie, n'empêche pas la
// page de se poser : seule compte la lecture encore en cours.
async function waitForSettledPage(page: Page, reads: Reads, startedAtLeast: number): Promise<void> {
  const deadline = Date.now() + pageLoadTimeout;
  let quietRenders = 0;
  while (quietRenders < 3) {
    if (Date.now() > deadline) {
      throw new Error(`la page ne se pose pas (${page.url()}) : ${reads.inFlight.size} lecture(s) en cours, ${reads.started} démarrée(s), ${startedAtLeast} attendue(s)`);
    }
    await waitForRenderedFrames(page);
    quietRenders = reads.inFlight.size === 0 && reads.started >= startedAtLeast ? quietRenders + 1 : 0;
  }
}

async function openPage(page: Page, path: string): Promise<void> {
  const reads = watchRequests(page);
  const startedBefore = reads.started;
  await page.goto(path, { timeout: pageLoadTimeout });
  await page.locator("main").waitFor({ state: "visible", timeout: pageLoadTimeout });
  // Toute page lit au moins l'accès et les compteurs : sans lecture démarrée,
  // la page n'a pas fini de s'ouvrir.
  await waitForSettledPage(page, reads, startedBefore + 1);
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
    if (action.menu === null) {
      findings.done.buttons++;
    } else {
      findings.done.menuChoices++;
    }
    await waitForSettledPage(page, watchRequests(page), 0);
    if ((await page.locator("dialog[open]").count()) > 0) {
      await exploreDialog(page, findings);
    }
  }
}

// Les adresses internes qu'une page propose, hors fenêtres et ancres.
export async function linksOf(page: Page, path: string): Promise<string[]> {
  await openPage(page, path);
  const hrefs = await page.locator("a[href^='/']").evaluateAll((anchors) => anchors.map((anchor) => anchor.getAttribute("href") ?? ""));
  return hrefs.map((href) => href.split("#")[0] ?? "").filter((href) => href !== "" && !href.startsWith("/api/") && !href.startsWith("/statut/") && !href.startsWith("/agent"));
}
