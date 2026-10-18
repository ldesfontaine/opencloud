import type { GapState } from "../api/types";
import { useT } from "../i18n/context";
import { gapTone } from "../lib/gaps";
import { Pill } from "./Pill";

// L'écart en pastille. Un rattachement faible s'ajoute à côté : il ne change
// pas l'état, il dit que le lien tient au seul nom du conteneur.
export function GapPill({ state, weak = false }: { state: GapState; weak?: boolean }) {
  const t = useT();
  return (
    <span className="cluster">
      <Pill tone={gapTone(state)}>{t(`gap.state_${state}`)}</Pill>
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
