import type { APITokenEnv, APIToken, APITokenRequest, APITokenScope } from "../api/apiTokens";
import type { Translate } from "../i18n/context";

// Une échéance offerte, en jours : « 90 jours », ou « Sans échéance » pour 0.
export function lifetimeLabel(t: Translate, days: number): string {
  return days === 0 ? t("api_token.lifetime_never") : t("api_token.lifetime_days", days);
}

// L'échéance d'un jeton dans la liste : sa date, ou « jamais ».
export function expiryLabel(t: Translate, expiresAt: string | null, formatDate: (iso: string) => string): string {
  return expiresAt === null ? t("api_token.expires_never") : formatDate(expiresAt);
}

// Le formulaire d'un jeton : son nom, son échéance en jours, sa portée et,
// pour l'écriture, les verbes cochés.
export interface TokenFormValues {
  name: string;
  days: number;
  scope: APITokenScope;
  verbs: string[];
}

// L'échéance part à celle que le serveur propose, la portée à la lecture, et
// aucun verbe n'est coché : l'opérateur choisit ce qu'il ouvre.
export function emptyTokenForm(defaultDays: number): TokenFormValues {
  return { name: "", days: defaultDays, scope: "read", verbs: [] };
}

// Les verbes cochés ne partent qu'avec l'écriture : repasser en lecture les
// oublie, le serveur refuserait la demande sinon.
export function tokenRequestOf(values: TokenFormValues): APITokenRequest {
  const request: APITokenRequest = { name: values.name, expires_in_days: values.days, scope: values.scope };
  if (values.scope === "write") {
    request.verbs = values.verbs;
  }
  return request;
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

// Les trois lignes que mcp-maison lit dans son .env : où il écoute, où il
// joint l'API en direct, et le jeton.
export function envBlock(env: APITokenEnv, token: string): string {
  return [`MCP_HOST=${env.mcp_host}`, `OPENCLOUD_URL=${env.opencloud_url}`, `OPENCLOUD_TOKEN=${token}`].join("\n");
}
