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

// La commande qui déclare openCloud à Claude Code, depuis celle du client
// local que le serveur fabrique.
export function claudeCodeCommand(stdioCommand: string): string {
  return `claude mcp add opencloud -- ${stdioCommand}`;
}
