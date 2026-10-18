import type { MCPScope } from "../api/types";
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

// « Un accès vaut 1 h, renouvelé jusqu'à 30 j » : les durées viennent du
// serveur en secondes, la phrase se fait ici.
export function lifetimesOf(t: Translate, accessSeconds: number, refreshSeconds: number): string {
  return t("mcp.lifetimes", formatDuration(t, accessSeconds * 1000), formatDuration(t, refreshSeconds * 1000));
}

// La commande qui déclare le serveur à Claude Code, sous le nom que le
// serveur donne, depuis celle du client local qu'il fabrique.
export function claudeCodeCommand(serverName: string, stdioCommand: string): string {
  return `claude mcp add ${serverName} -- ${stdioCommand}`;
}

// Les portées, lecture seule d'abord : c'est le choix par défaut.
export const mcpScopes: MCPScope[] = ["read", "write"];

// Une échéance offerte, en jours : « 90 jours », ou « Sans échéance » pour 0.
export function lifetimeLabel(t: Translate, days: number): string {
  return days === 0 ? t("mcp.lifetime_never") : t("mcp.lifetime_days", days);
}

// L'échéance d'un jeton dans la liste : sa date, ou « jamais ».
export function expiryLabel(t: Translate, expiresAt: string | null, formatDate: (iso: string) => string): string {
  return expiresAt === null ? t("mcp.expires_never") : formatDate(expiresAt);
}
