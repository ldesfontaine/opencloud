import { useState, type ComponentProps, type ReactNode } from "react";

import { Button } from "./Button";
import type { IconName } from "./Icon";
import { Modal, ModalCancel } from "./Modal";

// La question d'un geste qui ne se défait pas (supprimer, régénérer un jeton,
// remplacer une clé), dans une fenêtre qui nomme la conséquence. Ce qui se
// défait reste à ConfirmInline, plus léger. onConfirm part dès le clic et la
// fenêtre se ferme : l'écriture et son erreur restent à la page (useAction),
// comme avec ConfirmInline. Contrôlée par l'appelant quand le geste n'est pas
// un bouton (un envoi de formulaire) ; ConfirmModal en est le bouton.
export function ConfirmDialog({
  title,
  question,
  confirmLabel,
  onConfirm,
  onClose,
}: {
  title: string;
  question: string;
  confirmLabel: string;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const confirm = () => {
    onClose();
    onConfirm();
  };
  return (
    <Modal
      title={title}
      onClose={onClose}
      actions={
        <>
          <ModalCancel />
          <Button variant="danger" onClick={confirm}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      <p className="modal-question">{question}</p>
    </Modal>
  );
}

// Un bouton qui ouvre la confirmation. Même forme que ConfirmInline :
// l'appelant change de balise, pas de logique.
export function ConfirmModal({
  title,
  question,
  confirmLabel,
  onConfirm,
  variant = "secondary",
  small = false,
  icon,
  disabled = false,
  children,
}: {
  title: string;
  question: string;
  confirmLabel: string;
  onConfirm: () => void;
  variant?: ComponentProps<typeof Button>["variant"];
  small?: boolean;
  icon?: IconName;
  disabled?: boolean;
  // Le libellé du bouton qui ouvre la fenêtre.
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant={variant} small={small} {...(icon !== undefined && { icon })} disabled={disabled} onClick={() => setOpen(true)}>
        {children}
      </Button>
      {open && <ConfirmDialog title={title} question={question} confirmLabel={confirmLabel} onConfirm={onConfirm} onClose={() => setOpen(false)} />}
    </>
  );
}
