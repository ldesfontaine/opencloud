import type { MCPConsent, MCPState, MCPTokenRequest } from "../api/mcp";
import type { Translate } from "../i18n/context";
import { formatDuration } from "./time";

// Les adresses de retour se saisissent une par ligne ; les vides et les
// espaces autour ne comptent pas, le serveur juge le reste.
export function redirectLines(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
}

// Les adresses de retour telles que la zone de texte les montre : une par
// ligne. Le serveur reçoit la liste nettoyée (redirectLines).
export interface RedirectFormValues {
  uris: string;
}

export function redirectFormOf(state: Pick<MCPState, "redirect_uris">): RedirectFormValues {
  return { uris: state.redirect_uris.join("\n") };
}

export function redirectRequestOf(values: RedirectFormValues): string[] {
  return redirectLines(values.uris);
}

// « Un accès vaut 1 h, renouvelé jusqu'à 30 j » : les durées viennent du
// serveur en secondes, la phrase se fait ici.
export function lifetimesOf(t: Translate, accessSeconds: number, refreshSeconds: number): string {
  return t("mcp.lifetimes", formatDuration(t, accessSeconds * 1000), formatDuration(t, refreshSeconds * 1000));
}

// Ce que la page de consentement dit de l'écriture : rien si le client ne
// la demande pas ; la case, décochée, si le serveur peut l'accorder ; sinon
// une note qui dit que seule la lecture sera accordée.
export type WriteOffer = "none" | "checkbox" | "unavailable";

export function writeOfferOf(consent: Pick<MCPConsent, "write_requested" | "write_available">): WriteOffer {
  if (!consent.write_requested) {
    return "none";
  }
  return consent.write_available ? "checkbox" : "unavailable";
}

// La commande qui déclare le serveur à Claude Code, sous le nom que le
// serveur donne, depuis celle du client local qu'il fabrique.
export function claudeCodeCommand(serverName: string, stdioCommand: string): string {
  return `claude mcp add ${serverName} -- ${stdioCommand}`;
}

// Une échéance offerte, en jours : « 90 jours », ou « Sans échéance » pour 0.
export function lifetimeLabel(t: Translate, days: number): string {
  return days === 0 ? t("mcp.lifetime_never") : t("mcp.lifetime_days", days);
}

// L'échéance d'un jeton dans la liste : sa date, ou « jamais ».
export function expiryLabel(t: Translate, expiresAt: string | null, formatDate: (iso: string) => string): string {
  return expiresAt === null ? t("mcp.expires_never") : formatDate(expiresAt);
}

// Le formulaire d'un jeton d'API : son nom et son échéance en jours. Pas de
// portée à choisir : le MCP HTTP est en lecture seule, et le serveur donne
// `read` à un jeton qui n'en dit pas.
export interface TokenFormValues {
  name: string;
  days: number;
}

// L'échéance part à celle que le serveur propose.
export function emptyTokenForm(defaultDays: number): TokenFormValues {
  return { name: "", days: defaultDays };
}

export function tokenRequestOf(values: TokenFormValues): MCPTokenRequest {
  return { name: values.name, expires_in_days: values.days };
}
