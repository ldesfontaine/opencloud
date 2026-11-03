import type { Write } from "../api/routes";
import { useIsVerbRoute, useT } from "../i18n/context";

// L'infobulle d'un geste que l'API ouvre aussi à un agent IA (un verbe du
// registre) : une phrase, au survol, plutôt qu'une puce sans sens pour qui ne
// connaît pas MCP. Undefined pour un geste réservé à l'interface.
export function useAgentHint(): (route: Write) => string | undefined {
  const t = useT();
  const isVerb = useIsVerbRoute();
  return (route) => (isVerb(route) ? t("action.agent_hint") : undefined);
}
