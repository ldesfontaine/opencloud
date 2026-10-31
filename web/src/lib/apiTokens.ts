import type { APITokenEnv, APIToken, APITokenRequest, APITokenScope, ReachMachine, ReachRequest } from "../api/apiTokens";
import type { Translate } from "../i18n/context";

// Une échéance offerte, en jours : « 90 jours », ou « Sans échéance » pour 0.
export function lifetimeLabel(t: Translate, days: number): string {
  return days === 0 ? t("api_token.lifetime_never") : t("api_token.lifetime_days", days);
}

// L'échéance d'un jeton dans la liste : sa date, ou « jamais ».
export function expiryLabel(t: Translate, expiresAt: string | null, formatDate: (iso: string) => string): string {
  return expiresAt === null ? t("api_token.expires_never") : formatDate(expiresAt);
}

export type ReachMode = "all" | "some";

// Où les verbes portent, tel que le formulaire le saisit : « toutes » ou une
// liste de machines cochées ; « tous » ou des numéros d'invités tapés.
export interface ReachFormValues {
  machinesMode: ReachMode;
  machines: string[];
  guestsMode: ReachMode;
  guests: string;
}

export function emptyReachForm(): ReachFormValues {
  return { machinesMode: "all", machines: [], guestsMode: "all", guests: "" };
}

// Le formulaire d'un jeton : son nom, son échéance en jours, sa portée,
// pour l'écriture les verbes cochés, et jusqu'où ils portent.
export interface TokenFormValues extends ReachFormValues {
  name: string;
  days: number;
  scope: APITokenScope;
  verbs: string[];
}

// L'échéance part à celle que le serveur propose, la portée à la lecture, et
// aucun verbe n'est coché : l'opérateur choisit ce qu'il ouvre. Les verbes
// portent partout tant que l'opérateur ne limite rien.
export function emptyTokenForm(defaultDays: number): TokenFormValues {
  return { name: "", days: defaultDays, scope: "read", verbs: [], ...emptyReachForm() };
}

// La portée d'un jeton existant, pour la modifier.
export function reachFormOf(token: APIToken): ReachFormValues {
  return {
    machinesMode: token.machines === null ? "all" : "some",
    machines: token.machines ?? [],
    guestsMode: token.guests === null ? "all" : "some",
    guests: (token.guests ?? []).join(" "),
  };
}

// Les verbes de Proxmox sont les seuls à viser un invité : sans l'un d'eux
// coché, le choix des invités n'a pas de sens.
export function hasProxmoxVerb(verbs: string[]): boolean {
  return verbs.some((verb) => verb.startsWith("proxmox."));
}

// Les numéros d'invités tapés, séparés par des espaces ou des virgules ; null
// si l'un n'est pas un entier de 1 à 999999999 (ce que le serveur accepte).
export function parseGuestNumbers(text: string): number[] | null {
  const words = text.split(/[\s,]+/).filter((word) => word !== "");
  const numbers: number[] = [];
  for (const word of words) {
    if (!/^\d{1,9}$/.test(word) || Number(word) < 1) {
      return null;
    }
    const number = Number(word);
    if (!numbers.includes(number)) {
      numbers.push(number);
    }
  }
  return numbers.sort((a, b) => a - b);
}

// La portée à envoyer : une clé n'est écrite que si l'opérateur limite.
// Les invités ne partent qu'avec un verbe Proxmox coché ; null si un numéro
// tapé est invalide.
export function reachRequestOf(values: ReachFormValues, withGuests: boolean): ReachRequest | null {
  const request: ReachRequest = {};
  if (values.machinesMode === "some") {
    request.machines = values.machines;
  }
  if (withGuests && values.guestsMode === "some") {
    const numbers = parseGuestNumbers(values.guests);
    if (numbers === null) {
      return null;
    }
    request.guests = numbers;
  }
  return request;
}

// Les verbes cochés et la portée ne partent qu'avec l'écriture : repasser en
// lecture les oublie, le serveur refuserait la demande sinon. Null si les
// numéros d'invités tapés sont invalides.
export function tokenRequestOf(values: TokenFormValues): APITokenRequest | null {
  const request: APITokenRequest = { name: values.name, expires_in_days: values.days, scope: values.scope };
  if (values.scope !== "write") {
    return request;
  }
  const reach = reachRequestOf(values, hasProxmoxVerb(values.verbs));
  if (reach === null) {
    return null;
  }
  return { ...request, verbs: values.verbs, ...reach };
}

export function toggleVerb(verbs: string[], id: string): string[] {
  return verbs.includes(id) ? verbs.filter((verb) => verb !== id) : [...verbs, id];
}

// La portée et les verbes d'un jeton dans la liste : « Lecture », ou
// « Écriture : Mettre une sonde en pause, … », ou « Écriture : aucun verbe ».
export function scopeLabel(t: Translate, token: APIToken, labelOf: (id: string) => string): string {
  if (token.scope === "read") {
    return t("api_token.scope_read");
  }
  const verbs = token.verbs.length === 0 ? t("api_token.verbs_none_list") : token.verbs.map(labelOf).join(", ");
  return `${t("api_token.scope_write")} : ${verbs}`;
}

// La portée d'un jeton en écriture dans la liste : une ligne pour les
// machines, une pour les invités quand il a un verbe Proxmox. Un identifiant que la
// liste ne connaît plus (machine supprimée) se montre tel quel.
export function reachLines(t: Translate, token: APIToken, machines: ReachMachine[]): string[] {
  if (token.scope !== "write") {
    return [];
  }
  const lines: string[] = [];
  if (token.machines === null) {
    lines.push(t("api_token.reach_machines_value", t("api_token.reach_every_machine")));
  } else {
    const names = token.machines.map((id) => machines.find((machine) => machine.id === id)?.name ?? id);
    lines.push(t("api_token.reach_machines_value", names.length === 0 ? t("api_token.reach_no_machine") : names.join(", ")));
  }
  // Les invités ne se disent que pour un jeton qui a un verbe Proxmox.
  if (token.guests !== null) {
    lines.push(t("api_token.reach_guests_value", token.guests.length === 0 ? t("api_token.reach_no_guest") : token.guests.join(", ")));
  } else if (hasProxmoxVerb(token.verbs)) {
    lines.push(t("api_token.reach_guests_value", t("api_token.reach_every_guest")));
  }
  return lines;
}

// Les trois lignes que mcp-maison lit dans son .env : où il écoute, où il
// joint l'API en direct, et le jeton.
export function envBlock(env: APITokenEnv, token: string): string {
  return [`MCP_HOST=${env.mcp_host}`, `OPENCLOUD_URL=${env.opencloud_url}`, `OPENCLOUD_TOKEN=${token}`].join("\n");
}
