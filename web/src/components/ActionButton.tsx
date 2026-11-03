import type { ReactNode } from "react";

import type { Write } from "../api/routes";
import { useAgentHint } from "../hooks/useAgentHint";
import { Button } from "./Button";
import type { IconName } from "./Icon";

// Le bouton principal d'une page : un seul par en-tête (doc 25, « Les
// actions »). Il dit la route d'API que son geste finit par appeler ; si cette
// route est un verbe du registre, un agent peut faire la même chose, et la
// puce « MCP » le dit. Un geste sans verbe n'a pas la puce.
// to : le geste s'ouvre à une adresse (une fenêtre modale) ; onClick : il part
// ici.
export function ActionButton({
  route,
  icon,
  to,
  onClick,
  disabled = false,
  children,
}: {
  route: Write;
  icon?: IconName;
  to?: string;
  onClick?: () => void;
  disabled?: boolean;
  children: ReactNode;
}) {
  const agentHint = useAgentHint()(route);
  return (
    <Button variant="primary" title={agentHint} {...(icon !== undefined && { icon })} {...(to !== undefined && { to })} {...(onClick !== undefined && { onClick })} disabled={disabled}>
      {children}
    </Button>
  );
}
