import type { VerbRequest } from "../api/verbRequests";
import type { Translate } from "../i18n/context";
import { formatRemaining } from "./time";

// La phrase d'une demande, pour les verbes que l'écran sait présenter : la
// clé de catalogue prend le nom de l'objet. Un verbe que l'écran ne connaît
// pas encore se montre par son identifiant : la demande reste lisible et
// refusable.
export function requestPhrase(t: Translate, request: VerbRequest): string {
  if (request.verb === "machine.delete") {
    return t("verb_request.machine_delete", request.object.name);
  }
  return `${request.verb} : ${request.object.name}`;
}

// Qui demande : le jeton, et pour qui il dit appeler quand mcp-maison l'a dit.
export function requestedBy(t: Translate, request: VerbRequest): string {
  if (request.on_behalf_of === "") {
    return t("verb_request.by", request.token_name);
  }
  return t("verb_request.by_for", request.token_name, request.on_behalf_of);
}

// Ce qu'il reste avant l'échéance ; passée, la demande ne se confirme plus.
export function requestExpiry(t: Translate, request: VerbRequest, now: number): string {
  const remaining = Date.parse(request.expires_at) - now;
  if (remaining <= 0) {
    return t("verb_request.expired_now");
  }
  return t("verb_request.expires_in", formatRemaining(t, remaining));
}
