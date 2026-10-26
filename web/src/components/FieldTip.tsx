import { useId, useState, type KeyboardEvent } from "react";

import { useT } from "../i18n/context";
import "./FieldTip.scss";

// Le « ? » d'un champ : le détail qu'on ne veut pas à l'écran. Il se prend
// au clavier comme à la souris, et le détail lui est rattaché
// (aria-describedby) pour être lu, même caché. Échap le referme sans
// déplacer le focus ; il revient au prochain survol ou au prochain focus.
export function FieldTip({ text }: { text: string }) {
  const t = useT();
  const tipID = useId();
  const [isDismissed, setDismissed] = useState(false);

  const dismissOnEscape = (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      setDismissed(true);
    }
  };

  return (
    <span className={isDismissed ? "field-tip field-tip-dismissed" : "field-tip"} onMouseLeave={() => setDismissed(false)}>
      <button
        type="button"
        className="field-tip-button"
        aria-label={t("field.tip_label")}
        aria-describedby={tipID}
        onKeyDown={dismissOnEscape}
        onBlur={() => setDismissed(false)}
      >
        ?
      </button>
      <span className="field-tip-text" role="tooltip" id={tipID}>
        {text}
      </span>
    </span>
  );
}
