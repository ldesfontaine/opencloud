import { useId, type FormEvent, type ReactNode } from "react";

import type { ApiError } from "../api/client";
import { Button } from "./Button";
import { Failure, type RightGesture } from "./Failure";
import type { IconName } from "./Icon";
import { Modal, ModalCancel } from "./Modal";

// Une fenêtre à une seule étape : les champs, l'erreur de l'écriture sous
// eux, et dans le pied « Annuler » et l'action. Quand il y a plusieurs
// étapes ou un récapitulatif à relire, c'est Wizard. L'action est liée au
// formulaire par l'attribut form : Entrée dans un champ l'envoie, comme le
// clic. canSubmit attend que la saisie le permette, busy la fin de
// l'écriture en cours.
export function ModalForm({
  title,
  onClose,
  dirty,
  wide = false,
  onSubmit,
  submitLabel,
  submitIcon,
  busy,
  canSubmit = true,
  error,
  errorGesture,
  children,
}: {
  title: string;
  onClose: () => void;
  dirty: boolean;
  wide?: boolean;
  onSubmit: () => void;
  submitLabel: string;
  submitIcon: IconName;
  busy: boolean;
  canSubmit?: boolean;
  error: ApiError | string | null;
  // Où mène un refus de droit : voir Failure.
  errorGesture?: RightGesture | undefined;
  children: ReactNode;
}) {
  const formID = useId();
  const submit = (event: FormEvent) => {
    event.preventDefault();
    onSubmit();
  };
  return (
    <Modal
      title={title}
      onClose={onClose}
      dirty={dirty}
      wide={wide}
      actions={
        <>
          <ModalCancel />
          <Button type="submit" form={formID} variant="primary" icon={submitIcon} disabled={busy || !canSubmit}>
            {submitLabel}
          </Button>
        </>
      }
    >
      <form id={formID} className="modal-form" onSubmit={submit}>
        {children}
        <Failure error={error} gesture={errorGesture} />
      </form>
    </Modal>
  );
}
