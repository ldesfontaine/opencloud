import { Link } from "react-router";

import { useT } from "../i18n/context";
import { TokenBox } from "./Copy";

// La commande à lancer en root sur la machine quand une ancre de confiance
// locale a fait refuser un ordre : l'unique endroit qui la montre (refus d'une
// requête, ordre refusé, sauvegarde refusée). exact : elle pose la valeur de
// l'ordre. Sinon c'est « trust list », qui montre ce que l'agent tient, avec un
// lien vers la fiche de la machine, onglet Agent (machineID absent : pas de lien).
export function AnchorFix({ command, exact, machineID }: { command: string; exact: boolean; machineID: string | null | undefined }) {
  const t = useT();
  return (
    <div className="anchor-fix">
      <p className="secondary">{t("anchor.run_command")}</p>
      <TokenBox value={command} />
      {!exact && machineID !== null && machineID !== undefined && machineID !== "" && <Link to={`/machines/${machineID}/agent`}>{t("anchor.see_machine")}</Link>}
    </div>
  );
}
