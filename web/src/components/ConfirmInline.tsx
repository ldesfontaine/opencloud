import { useEffect, useRef, useState, type ComponentProps, type KeyboardEvent, type ReactNode } from "react";

import { useT } from "../i18n/context";
import { nextConfirm, returnsFocus, shouldConfirm, type ConfirmEvent, type ConfirmState } from "../lib/confirmInline";
import { Button } from "./Button";
import type { IconName } from "./Icon";
import "./ConfirmInline.scss";

// Une confirmation en ligne pour une action d'une ligne : le bouton se replie
// en « question [Confirmer] [Annuler] » à sa place, le focus sur « Annuler »,
// Échap annule. Pour un plan à lire avant de supprimer, la boîte
// DeletePlanDialog reste.
export function ConfirmInline({
  question,
  onConfirm,
  variant = "secondary",
  small = false,
  icon,
  disabled = false,
  children,
}: {
  question: string;
  onConfirm: () => void;
  variant?: ComponentProps<typeof Button>["variant"];
  small?: boolean;
  icon?: IconName;
  disabled?: boolean;
  children: ReactNode;
}) {
  const t = useT();
  const [state, setState] = useState<ConfirmState>("idle");
  const root = useRef<HTMLSpanElement>(null);
  const refocus = useRef(false);

  // Le bouton d'origine revient après la question : le focus y retourne.
  useEffect(() => {
    if (state === "idle" && refocus.current) {
      refocus.current = false;
      root.current?.querySelector("button")?.focus();
    }
  }, [state]);

  const send = (event: ConfirmEvent) => {
    const after = nextConfirm(state, event);
    if (shouldConfirm(state, event)) {
      onConfirm();
    }
    refocus.current = returnsFocus(state, after);
    setState(after);
  };
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === "Escape") {
      event.stopPropagation();
      send("escape");
    }
  };

  return (
    <span className="confirm-inline" ref={root}>
      {state === "idle" ? (
        <Button variant={variant} small={small} {...(icon !== undefined && { icon })} disabled={disabled} onClick={() => send("ask")}>
          {children}
        </Button>
      ) : (
        <span className="confirm-ask" role="group" aria-label={question} onKeyDown={onKeyDown}>
          <span className="confirm-question">{question}</span>
          <span className="confirm-buttons">
            <Button variant="danger" small onClick={() => send("confirm")}>
              {t("confirm.yes")}
            </Button>
            <Button variant="secondary" small autoFocus onClick={() => send("cancel")}>
              {t("confirm.cancel")}
            </Button>
          </span>
        </span>
      )}
    </span>
  );
}
