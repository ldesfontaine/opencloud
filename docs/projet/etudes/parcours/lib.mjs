// Outillage du parcours : un navigateur, un chronomètre, un compteur de clics.
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const require = createRequire(path.resolve(here, "../../../../web/package.json"));
const { chromium } = require("@playwright/test");

export const BASE = process.env.OC_URL ?? "http://127.0.0.1:18480";
export const SHOTS = process.env.OC_SHOTS ?? path.join(here);

export async function openBrowser() {
  const browser = await chromium.launch();
  const credentials = process.env.OC_USER ? { username: process.env.OC_USER, password: process.env.OC_PASSWORD } : undefined;
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, locale: "fr-FR", httpCredentials: credentials });
  const page = await context.newPage();
  page.setDefaultTimeout(5000);
  return { browser, context, page };
}

// Une tâche : durée, clics, retours en arrière. Les clics sont comptés sur la page.
export class Task {
  constructor(page, name) {
    this.page = page; this.name = name; this.clicks = 0; this.backs = 0; this.guesses = []; this.t0 = Date.now();
  }
  async click(target, label) {
    this.clicks++;
    await (typeof target === "string" ? this.page.locator(target).first() : target).click();
    if (label) this.last = label;
  }
  guess(note) { this.guesses.push(note); }
  back() { this.backs++; }
  async shot(file, options = {}) { await shot(this.page, file, options); }
  end() {
    const s = ((Date.now() - this.t0) / 1000).toFixed(1);
    const r = { task: this.name, seconds: s, clicks: this.clicks, backs: this.backs, guesses: this.guesses };
    console.log(JSON.stringify(r));
    return r;
  }
}

export async function bodyText(page) {
  return (await page.locator("body").innerText()).replace(/\n{3,}/g, "\n\n");
}

// Une capture sans secret : les jetons d'enrôlement (oc_…) sont masqués dans
// le texte de la page avant la prise de vue, et rendus après.
export async function shot(page, file, { full = false } = {}) {
  await page.evaluate(() => {
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    const saved = [];
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      if (/(oc|hb)_[a-z0-9]{20,}/.test(node.nodeValue)) {
        saved.push([node, node.nodeValue]);
        node.nodeValue = node.nodeValue.replace(/(oc|hb)_[a-z0-9]{20,}/g, "••••••••••••••••••••");
      }
    }
    window.__masked = saved;
  });
  await page.screenshot({ path: path.join(SHOTS, file), fullPage: full });
  await page.evaluate(() => { for (const [node, value] of window.__masked ?? []) node.nodeValue = value; });
}

// Ouvre chaque menu déroulant visible et dit si sa liste est bien au premier
// plan (le point sous chaque option appartient à la liste) et dans la fenêtre.
export async function checkDropdowns(page, where) {
  const results = [];
  const inDialog = (await page.locator('dialog[open], [role="dialog"]:visible').count()) > 0;
  const triggers = page.locator(inDialog ? 'dialog[open] [role="combobox"]:visible, [role="dialog"] [role="combobox"]:visible' : 'main [role="combobox"]:visible');
  const count = await triggers.count();
  for (let index = 0; index < count; index++) {
    const trigger = triggers.nth(index);
    let label;
    try {
      label = ((await trigger.getAttribute("aria-label", { timeout: 1500 })) ?? (await trigger.innerText({ timeout: 1500 })).split("\n")[0]).trim();
      await trigger.click({ timeout: 1500 });
    } catch { continue; }
    await page.waitForTimeout(250);
    const verdict = await page.evaluate(() => {
      const options = [...document.querySelectorAll('[role="option"]')].filter((o) => o.getBoundingClientRect().width > 0);
      if (options.length === 0) return { options: 0, covered: 0, outside: 0 };
      let covered = 0;
      let outside = 0;
      for (const option of options) {
        const box = option.getBoundingClientRect();
        const top = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
        if (!top || !(option === top || option.contains(top) || top.contains(option))) covered++;
        if (box.bottom > innerHeight || box.top < 0) outside++;
      }
      return { options: options.length, covered, outside };
    });
    results.push({ where, label, ...verdict });
    await page.keyboard.press("Escape");
    await page.waitForTimeout(150);
  }
  return results;
}

// Les blocs d'aide longs : les paragraphes de plus de 25 mots, et les
// renvois de menu (« Paramètres › Accès ») qui ne sont pas des liens.
export async function proseStats(page) {
  return page.evaluate(() => {
    // Une fenêtre ouverte masque la page : on ne lit qu'elle.
    const root = document.querySelector("dialog[open], [role=dialog]") ?? document.querySelector("main") ?? document.body;
    const long = [];
    for (const element of root.querySelectorAll("p, li, small, span, div")) {
      if (element.getBoundingClientRect().width === 0) continue;
      if ([...element.children].some((child) => child.innerText?.trim().length > 40)) continue;
      const words = element.innerText.trim().split(/\s+/).filter(Boolean).length;
      if (words > 25 && !element.closest("pre, code")) long.push({ words, text: element.innerText.trim().slice(0, 80) });
    }
    const dead = [];
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const match = /(Paramètres|Alertes|Machines|Services|Sauvegardes|Disponibilité|Journal|fiche de la machine|Ré-enrôler)\s*(›|→)\s*[^.;)\n]{2,40}/.exec(node.nodeValue);
      if (match && !node.parentElement.closest("a, button")) dead.push(match[0]);
    }
    return { long, dead };
  });
}
