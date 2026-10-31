import { createContext, useContext, useEffect, useId, useRef, useState, type ReactNode, type SyntheticEvent } from "react";

import { useT } from "../i18n/context";
import { nextModal, type ModalEvent, type ModalState } from "../lib/modal";
import { Button } from "./Button";
import { ConfirmInline } from "./ConfirmInline";
import "./Modal.scss";

const ModalContext = createContext<(() => void) | null>(null);

// Ce que le contenu d'une fenêtre appelle pour la fermer comme le fait Échap :
// directement si la saisie est propre, après « Abandonner la saisie ? » sinon.
function useModalClose(): () => void {
  const requestClose = useContext(ModalContext);
  if (requestClose === null) {
    throw new Error("useModalClose outside Modal");
  }
  return requestClose;
}

// Le bouton « Annuler » d'une fenêtre.
export function ModalCancel() {
  const t = useT();
  const requestClose = useModalClose();
  return <Button onClick={requestClose}>{t("modal.cancel")}</Button>;
}

// La fenêtre modale : un <dialog> natif, qui piège le focus et rend le reste
// de la page inerte. Le voile flou ne ferme pas : un clic à côté ne doit
// jamais perdre une saisie. Échap, la croix et « Annuler » ferment ; si dirty
// (la saisie a changé), la question « Abandonner la saisie ? » passe avant.
// onClose dit à l'appelant de la démonter, en général en revenant à
// l'adresse d'arrière-plan. Elle est montée pour un usage et démontée à la
// fermeture : rien de ce qui y est tapé ne survit.
export function Modal({
  title,
  onClose,
  dirty = false,
  wide = false,
  actions,
  children,
}: {
  title: string;
  onClose: () => void;
  dirty?: boolean;
  wide?: boolean;
  // Le pied de la fenêtre : ses boutons. Remplacé par la question d'abandon
  // pendant qu'elle est posée.
  actions?: ReactNode;
  children: ReactNode;
}) {
  const t = useT();
  const dialog = useRef<HTMLDialogElement>(null);
  const titleID = useId();
  const [state, setState] = useState<ModalState>("open");
  // Ce qui avait le focus avant la question : il y revient quand on continue.
  const focusedBefore = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (dialog.current !== null && !dialog.current.open) {
      dialog.current.showModal();
    }
  }, []);

  useEffect(() => {
    if (state === "open" && focusedBefore.current !== null) {
      focusedBefore.current.focus();
      focusedBefore.current = null;
    }
  }, [state]);

  const send = (event: ModalEvent) => {
    const step = nextModal(state, event, dirty);
    if (step.state === "asking" && document.activeElement instanceof HTMLElement) {
      focusedBefore.current = document.activeElement;
    }
    setState(step.state);
    if (step.closes) {
      onClose();
    }
  };
  const requestClose = () => send("request_close");

  // Échap : le navigateur fermerait la fenêtre de lui-même, saisie comprise.
  const onCancel = (event: SyntheticEvent) => {
    event.preventDefault();
    requestClose();
  };

  return (
    <dialog ref={dialog} className={wide ? "modal modal-wide" : "modal"} aria-labelledby={titleID} onCancel={onCancel}>
      <ModalContext.Provider value={requestClose}>
        <header className="modal-head">
          <h2 className="modal-title" id={titleID}>
            {title}
          </h2>
          <Button variant="ghost" small icon="x" aria-label={t("modal.close")} onClick={requestClose} />
        </header>
        <div className="modal-body">{children}</div>
        {state === "asking" ? (
          <footer className="modal-foot">
            <ConfirmInline asking question={t("modal.abandon_question")} onConfirm={() => send("abandon")} onCancel={() => send("keep")}>
              {t("modal.cancel")}
            </ConfirmInline>
          </footer>
        ) : (
          actions !== undefined && <footer className="modal-foot">{actions}</footer>
        )}
      </ModalContext.Provider>
    </dialog>
  );
}
