import type { APITokenEnv, APITokenRequest } from "../api/apiTokens";
import type { Translate } from "../i18n/context";

// Une échéance offerte, en jours : « 90 jours », ou « Sans échéance » pour 0.
export function lifetimeLabel(t: Translate, days: number): string {
  return days === 0 ? t("api_token.lifetime_never") : t("api_token.lifetime_days", days);
}

// L'échéance d'un jeton dans la liste : sa date, ou « jamais ».
export function expiryLabel(t: Translate, expiresAt: string | null, formatDate: (iso: string) => string): string {
  return expiresAt === null ? t("api_token.expires_never") : formatDate(expiresAt);
}

// Le formulaire d'un jeton : son nom et son échéance en jours. Pas de portée
// à choisir : un jeton ne fait que lire, et le serveur donne `read` à un
// jeton qui n'en dit pas.
export interface TokenFormValues {
  name: string;
  days: number;
}

// L'échéance part à celle que le serveur propose.
export function emptyTokenForm(defaultDays: number): TokenFormValues {
  return { name: "", days: defaultDays };
}

export function tokenRequestOf(values: TokenFormValues): APITokenRequest {
  return { name: values.name, expires_in_days: values.days };
}

// Les trois lignes que mcp-maison lit dans son .env : où il écoute, où il
// joint l'API en direct, et le jeton.
export function envBlock(env: APITokenEnv, token: string): string {
  return [`MCP_HOST=${env.mcp_host}`, `OPENCLOUD_URL=${env.opencloud_url}`, `OPENCLOUD_TOKEN=${token}`].join("\n");
}
