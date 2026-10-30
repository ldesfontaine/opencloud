import type { VerbOffer } from "../../api/apiTokens";
import { useT } from "../../i18n/context";
import "./VerbChoices.scss";

// Les verbes qu'un jeton en écriture peut déclencher, à cocher : le libellé,
// une phrase de ce que le verbe fait, et celui qui le défait. Rien n'est
// coché d'avance.
export function VerbChoices({ offers, chosen, onToggle }: { offers: VerbOffer[]; chosen: string[]; onToggle: (id: string) => void }) {
  const t = useT();
  const labelOf = (id: string) => {
    const found = offers.find((offer) => offer.id === id);
    return found === undefined ? id : t(found.label);
  };
  return (
    <fieldset className="verb-choices">
      <legend className="field-label">{t("api_token.verbs_label")}</legend>
      {offers.map((offer) => (
        <label key={offer.id} className="verb-choice">
          <input type="checkbox" checked={chosen.includes(offer.id)} onChange={() => onToggle(offer.id)} />
          <span className="verb-choice-text">
            <span className="verb-choice-label">{t(offer.label)}</span>
            <span className="secondary">{t(offer.summary)}</span>
            <span className="secondary">
              {t("api_token.verb_reverse", labelOf(offer.reverse))} <span className="mono">{offer.reverse}</span>
            </span>
          </span>
        </label>
      ))}
      {chosen.length === 0 && <span className="secondary">{t("api_token.verbs_none")}</span>}
    </fieldset>
  );
}
