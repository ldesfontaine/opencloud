import type { Write } from "../api/routes";
import { useIsVerbRoute, useT } from "../i18n/context";
import "./McpChip.scss";

// La puce « MCP » d'un geste : si la route qu'il appelle est un verbe du
// registre, un agent peut faire la même chose par un jeton. Rien sinon.
export function McpChip({ route }: { route: Write }) {
  const t = useT();
  const isVerb = useIsVerbRoute();
  if (!isVerb(route)) {
    return null;
  }
  return (
    <span className="mcp-chip" title={t("action.mcp_title")}>
      {t("action.mcp_chip")}
    </span>
  );
}
