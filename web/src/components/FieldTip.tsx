import { useId, useRef, useState, type KeyboardEvent } from "react";

import { useT } from "../i18n/context";
import "./FieldTip.scss";

// Le plus proche ancêtre qui coupe ce qui le dépasse (le corps défilant d'une
// fenêtre) : au-delà, une info-bulle est rognée.
function clippingAncestor(element: HTMLElement): HTMLElement | null {
  for (let parent = element.parentElement; parent !== null; parent = parent.parentElement) {
    if (getComputedStyle(parent).overflowY !== "visible") {
      return parent;
    }
  }
  return null;
}

// Le « ? » d'un champ : le détail qu'on ne veut pas à l'écran. Il se prend
// au clavier comme à la souris, et le détail lui est rattaché
// (aria-describedby) pour être lu, même caché. Échap le referme sans
// déplacer le focus ; il revient au prochain survol ou au prochain focus.
export function FieldTip({ text }: { text: string }) {
  const t = useT();
  const tipID = useId();
  const [isDismissed, setDismissed] = useState(false);
  const [opensBelow, setOpensBelow] = useState(false);
  const tipBox = useRef<HTMLSpanElement>(null);
  const bubble = useRef<HTMLSpanElement>(null);

  // L'info-bulle s'ouvre au-dessus ; sur un champ en haut d'une fenêtre il n'y
  // a pas la place, elle passe dessous.
  const placeTip = () => {
    if (tipBox.current === null || bubble.current === null) {
      return;
    }
    const room = clippingAncestor(tipBox.current)?.getBoundingClientRect().top ?? 0;
    const needed = tipBox.current.getBoundingClientRect().top - bubble.current.offsetHeight - 8;
    setOpensBelow(needed < room);
  };

  const dismissOnEscape = (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      setDismissed(true);
    }
  };

  return (
    <span
      ref={tipBox}
      className={["field-tip", isDismissed ? "field-tip-dismissed" : "", opensBelow ? "field-tip-below" : ""].filter(Boolean).join(" ")}
      onMouseEnter={placeTip}
      onFocus={placeTip}
      onMouseLeave={() => setDismissed(false)}
    >
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
      <span ref={bubble} className="field-tip-text" role="tooltip" id={tipID}>
        {text}
      </span>
    </span>
  );
}
