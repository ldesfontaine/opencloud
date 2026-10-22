import type { GapState } from "../api/directory";
import { useT } from "../i18n/context";
import { gapTone } from "../lib/gaps";
import { Pill } from "./Pill";

// L'écart en pastille. Un rattachement faible s'ajoute à côté : il ne change
// pas l'état, il dit que le lien tient au seul nom du conteneur. Quand la
// machine du service ne signale plus (observed faux), l'écart date d'avant son
// silence : la pastille dit « Inconnu », comme celle d'un conteneur.
export function GapPill({ state, weak = false, observed = true }: { state: GapState; weak?: boolean; observed?: boolean }) {
  const t = useT();
  return (
    <span className="cluster">
      {observed ? <Pill tone={gapTone(state)}>{t(`gap.state_${state}`)}</Pill> : <Pill tone="neutral">{t("container.state_unknown")}</Pill>}
      {weak && (
        <span title={t("gap.weak_help")}>
          <Pill tone="neutral" dot={false}>
            {t("gap.weak")}
          </Pill>
        </span>
      )}
    </span>
  );
}
